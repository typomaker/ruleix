package ruleix_test

import (
	"encoding/csv"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type productionLoadResult struct {
	implementation string
	rate, lookups  int
	duration       time.Duration
	latencies      []time.Duration
	completed      uint64
	allocBytes     uint64
	mallocs, gc    uint64
}

func TestProductionRequestLoad(t *testing.T) {
	if os.Getenv("RULEIX_LOAD") == "" {
		t.Skip("set RULEIX_LOAD=1 to run the saturation runner")
	}
	rates := productionCSVInts(t, "RULEIX_LOAD_RATES", []int{100, 300, 700, 1000, 2000})
	lookups := productionCSVInts(t, "RULEIX_LOAD_LOOKUPS", []int{10, 50, 100})
	duration, err := time.ParseDuration(productionEnv("RULEIX_LOAD_DURATION", "2s"))
	if err != nil {
		t.Fatal(err)
	}
	factories, cleanup := productionMatcherFactories(t, productionBenchmarkEntries)
	defer cleanup()
	requested := productionEnv("RULEIX_LOAD_IMPLEMENTATIONS", "LinearNatural,LinearOptimized,HandwrittenBitmap,RuleixLocal")
	allowed := make(map[string]bool)
	for _, name := range strings.Split(requested, ",") {
		allowed[name] = true
	}
	output := productionEnv("RULEIX_LOAD_OUTPUT", "/tmp/production-request-load.csv")
	file, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w := csv.NewWriter(file)
	defer w.Flush()
	t.Logf("writing load results to %s", output)
	_ = w.Write([]string{"implementation", "rate_target", "lookups_per_request", "requests_completed", "requests_per_sec", "lookups_per_sec", "p50_us", "p90_us", "p99_us", "p999_us", "max_us", "bytes_per_request", "allocs_per_request", "gc_cycles"})
	for _, factory := range factories {
		if !allowed[factory.name] {
			continue
		}
		for _, n := range lookups {
			for _, rate := range rates {
				result := runProductionLoad(factory, rate, n, duration)
				_ = w.Write(result.record())
			}
		}
	}
}

type productionLoadJob struct {
	sequence int
	arrived  time.Time
}

func runProductionLoad(factory productionMatcherFactory, rate, lookups int, duration time.Duration) productionLoadResult {
	queries := productionRequestQueries("Correlated", false)
	jobs := make(chan productionLoadJob, rate)
	latencies := make(chan time.Duration, rate)
	stop := make(chan struct{})
	var completed atomic.Uint64
	var wg sync.WaitGroup
	var collected sync.WaitGroup
	values := make([]time.Duration, 0, rate*int(duration/time.Second))
	collected.Add(1)
	go func() {
		defer collected.Done()
		for latency := range latencies {
			values = append(values, latency)
		}
	}()
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func(matcher productionRequestMatcher) {
			defer wg.Done()
			results := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			for {
				var job productionLoadJob
				select {
				case <-stop:
					return
				case next := <-jobs:
					job = next
				}
				base := job.sequence * lookups % len(queries)
				for j := range lookups {
					results = results[:0]
					matcher.Match(queries[(base+j)%len(queries)], &results)
				}
				latencies <- time.Since(job.arrived)
				completed.Add(1)
			}
		}(factory.new())
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	interval := time.Second / time.Duration(rate)
	ticker, deadline := time.NewTicker(interval), time.NewTimer(duration)
	sequence := 0
send:
	for {
		select {
		case now := <-ticker.C:
			select {
			case jobs <- productionLoadJob{sequence, now}:
			default:
			}
			sequence++
		case <-deadline.C:
			break send
		}
	}
	ticker.Stop()
	close(stop)
	wg.Wait()
	close(latencies)
	collected.Wait()
	runtime.ReadMemStats(&after)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return productionLoadResult{factory.name, rate, lookups, duration, values, completed.Load(),
		after.TotalAlloc - before.TotalAlloc, after.Mallocs - before.Mallocs, uint64(after.NumGC - before.NumGC)}
}

func (r productionLoadResult) percentile(fraction float64) time.Duration {
	if len(r.latencies) == 0 {
		return 0
	}
	index := int(float64(len(r.latencies)-1) * fraction)
	return r.latencies[index]
}

func (r productionLoadResult) record() []string {
	perSecond := float64(r.completed) / r.duration.Seconds()
	perRequest := func(total uint64) string {
		if r.completed == 0 {
			return "0"
		}
		return fmt.Sprintf("%.2f", float64(total)/float64(r.completed))
	}
	us := func(value time.Duration) string { return fmt.Sprintf("%.3f", float64(value.Nanoseconds())/1000) }
	maximum := time.Duration(0)
	if len(r.latencies) > 0 {
		maximum = r.latencies[len(r.latencies)-1]
	}
	return []string{r.implementation, strconv.Itoa(r.rate), strconv.Itoa(r.lookups), strconv.FormatUint(r.completed, 10),
		fmt.Sprintf("%.2f", perSecond), fmt.Sprintf("%.2f", perSecond*float64(r.lookups)), us(r.percentile(.50)),
		us(r.percentile(.90)), us(r.percentile(.99)), us(r.percentile(.999)), us(maximum), perRequest(r.allocBytes),
		perRequest(r.mallocs), strconv.FormatUint(r.gc, 10)}
}

func productionCSVInts(t *testing.T, name string, fallback []int) []int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	var result []int
	for _, raw := range strings.Split(value, ",") {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			t.Fatalf("%s: invalid %q", name, raw)
		}
		result = append(result, parsed)
	}
	return result
}

func productionEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
