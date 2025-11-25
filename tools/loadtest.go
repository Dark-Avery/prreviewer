package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type scenario struct {
	name    string
	prepare func(client *http.Client) error
	build   func(ctx context.Context, iter uint64) (*http.Request, error)
}

type metrics struct {
	reqs      uint64
	errs      uint64
	statuses  map[int]uint64
	errCounts map[string]uint64
	latencies []time.Duration
	mu        sync.Mutex
}

const (
	defaultDuration    = 5 * time.Second
	defaultConcurrency = 20
	defaultWarmup      = 1 * time.Second
)

var (
	baseURL      = getenv("BASE_URL", "http://localhost:8080")
	duration     = getenvDuration("DURATION", defaultDuration)
	concurrency  = getenvInt("CONCURRENCY", defaultConcurrency)
	warmup       = getenvDuration("WARMUP", defaultWarmup)
	runID        = time.Now().UnixNano()
	baseTeamName = fmt.Sprintf("loadteam-%d", runID)
	prSeq        uint64
	teamSeq      uint64
)

func main() {
	if os.Getenv("RESET_DB") == "1" {
		if err := resetDB(); err != nil {
			fmt.Printf("reset db failed: %v\n", err)
		} else {
			fmt.Println("DB reset done")
		}
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency * 4,
			MaxIdleConnsPerHost: concurrency * 4,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	scenarios := []scenario{
		{name: "health", build: buildHealth},
		{name: "stats", build: buildStats},
		{name: "team_add", build: buildTeamAdd},
		{name: "team_get", prepare: ensureBaseTeam, build: buildTeamGet},
		{name: "pr_create", prepare: ensureBaseTeam, build: buildPRCreate},
		{name: "mixed", prepare: ensureBaseTeam, build: buildMixed},
	}

	for _, sc := range scenarios {
		fmt.Printf("\n=== Scenario: %s ===\n", sc.name)
		runScenario(client, sc)
	}
}

func runScenario(client *http.Client, sc scenario) {
	if sc.prepare != nil {
		if err := sc.prepare(client); err != nil {
			fmt.Printf("prepare failed: %v\n", err)
			return
		}
	}

	metrics := &metrics{
		statuses:  make(map[int]uint64),
		errCounts: make(map[string]uint64),
	}

	if warmup > 0 {
		fmt.Printf("Warmup for %s...\n", warmup)
		wctx, cancel := context.WithTimeout(context.Background(), warmup)
		runWorkers(wctx, client, sc.build, metrics)
		cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	start := time.Now()
	runWorkers(ctx, client, sc.build, metrics)
	<-ctx.Done()
	elapsed := time.Since(start)
	cancel()

	printMetrics(metrics, elapsed)
}

func runWorkers(ctx context.Context, client *http.Client, build func(ctx context.Context, iter uint64) (*http.Request, error), m *metrics) {
	var wg sync.WaitGroup
	var iter uint64
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				n := atomic.AddUint64(&iter, 1)
				req, err := build(ctx, n)
				if err != nil {
					incrementErrors(&m.errs, m.errCounts, &m.mu, err, nil)
					continue
				}
				start := time.Now()
				resp, err := client.Do(req)
				latency := time.Since(start)
				if err != nil || resp == nil || resp.StatusCode >= http.StatusInternalServerError {
					incrementErrors(&m.errs, m.errCounts, &m.mu, err, resp)
				}
				recordStatus(&m.mu, m.statuses, resp)
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				m.mu.Lock()
				m.latencies = append(m.latencies, latency)
				m.mu.Unlock()
				atomic.AddUint64(&m.reqs, 1)
			}
		}()
	}
	wg.Wait()
}

func printMetrics(m *metrics, elapsed time.Duration) {
	total := atomic.LoadUint64(&m.reqs)
	fail := atomic.LoadUint64(&m.errs)
	rps := float64(total) / elapsed.Seconds()

	min, max, p50, p95, p99 := latencyStats(m.latencies)

	fmt.Printf("Duration: %s, Concurrency: %d\n", elapsed.Round(time.Millisecond), concurrency)
	fmt.Printf("Total requests: %d\n", total)
	fmt.Printf("Errors/5xx: %d\n", fail)
	fmt.Printf("RPS: %.2f\n", rps)
	fmt.Printf("Latency: min=%s, max=%s, p50=%s, p95=%s, p99=%s\n", min, max, p50, p95, p99)

	if len(m.statuses) > 0 {
		fmt.Println("Status codes:")
		for _, code := range sortedKeys(m.statuses) {
			fmt.Printf("  %d: %d\n", code, m.statuses[code])
		}
	}
	if len(m.errCounts) > 0 {
		fmt.Println("Errors:")
		for _, key := range sortedStrKeys(m.errCounts) {
			fmt.Printf("  %s: %d\n", key, m.errCounts[key])
		}
	}
}

func latencyStats(latencies []time.Duration) (min, max, p50, p95, p99 time.Duration) {
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	min = latencies[0]
	max = latencies[len(latencies)-1]
	p50 = percentile(latencies, 0.50)
	p95 = percentile(latencies, 0.95)
	p99 = percentile(latencies, 0.99)
	return
}

func percentile(latencies []time.Duration, p float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(latencies)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(latencies) {
		rank = len(latencies) - 1
	}
	return latencies[rank]
}

func buildHealth(ctx context.Context, _ uint64) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", http.NoBody)
}

func buildStats(ctx context.Context, _ uint64) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/stats", http.NoBody)
}

func buildTeamGet(ctx context.Context, _ uint64) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/team/get?team_name="+baseTeamName, http.NoBody)
}

func buildTeamAdd(ctx context.Context, iter uint64) (*http.Request, error) {
	name := fmt.Sprintf("loadteam-%d-%d", runID, atomic.AddUint64(&teamSeq, 1))
	body := fmt.Sprintf(`{"team_name":"%s","members":[{"user_id":"%s-u1","username":"Member","is_active":true}]}`, name, name)
	return newJSONRequest(ctx, http.MethodPost, baseURL+"/team/add", body)
}

func buildPRCreate(ctx context.Context, _ uint64) (*http.Request, error) {
	id := fmt.Sprintf("pr-%d-%d", runID, atomic.AddUint64(&prSeq, 1))
	body := fmt.Sprintf(`{"pull_request_id":"%s","pull_request_name":"Load Test","author_id":"u1"}`, id)
	return newJSONRequest(ctx, http.MethodPost, baseURL+"/pullRequest/create", body)
}

func buildMixed(ctx context.Context, iter uint64) (*http.Request, error) {
	switch iter % 4 {
	case 0:
		return buildHealth(ctx, iter)
	case 1:
		return buildStats(ctx, iter)
	case 2:
		return buildTeamGet(ctx, iter)
	default:
		return buildPRCreate(ctx, iter)
	}
}

func ensureBaseTeam(client *http.Client) error {
	body := fmt.Sprintf(`{"team_name":"%s","members":[{"user_id":"u1","username":"Alice","is_active":true},{"user_id":"u2","username":"Bob","is_active":true}]}`, baseTeamName)
	req, err := newJSONRequest(context.Background(), http.MethodPost, baseURL+"/team/add", body)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("ensure team failed: status %d", resp.StatusCode)
	}
	return nil
}

func newJSONRequest(ctx context.Context, method, url, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if out, err := strconv.Atoi(v); err == nil && out > 0 {
			return out
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func incrementErrors(
	errs *uint64,
	errCounts map[string]uint64,
	mu *sync.Mutex,
	err error,
	resp *http.Response,
) {
	if errs != nil {
		atomic.AddUint64(errs, 1)
	}
	if errCounts == nil || mu == nil {
		return
	}
	mu.Lock()
	errKey := categorizeError(err, resp)
	errCounts[errKey]++
	mu.Unlock()
}

func recordStatus(mu *sync.Mutex, statuses map[int]uint64, resp *http.Response) {
	if resp == nil || statuses == nil || mu == nil {
		return
	}
	mu.Lock()
	statuses[resp.StatusCode]++
	mu.Unlock()
}

func sortedKeys(m map[int]uint64) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedStrKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func categorizeError(err error, resp *http.Response) string {
	const unknown = "unknown"
	if resp != nil && resp.StatusCode >= 500 {
		return fmt.Sprintf("http_%d", resp.StatusCode)
	}
	if err == nil {
		return unknown
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection reset by peer"):
		return "conn_reset"
	case strings.Contains(msg, "connection refused"):
		return "conn_refused"
	case strings.Contains(msg, "context deadline exceeded"):
		return "timeout"
	default:
		return msg
	}
}

func resetDB() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL required for RESET_DB")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(context.Background(), `
TRUNCATE assigned_reviewers, pull_requests, users, teams RESTART IDENTITY CASCADE;
`)
	return err
}
