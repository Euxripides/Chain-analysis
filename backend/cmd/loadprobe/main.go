// Command loadprobe 是 S7-PERF-01 的分层压测工具：对真实 API 发压并给出可复现的分位数。
//
// 设计：
//   - 只用标准库（net/http + strings + sort），无第三方依赖，便于在任意环境重建；
//   - 输出 P50/P90/P95/P99、吞吐、状态码分布、错误分类，并可写 JSON 证据文件；
//   - 每条请求都进入样本集，分位数由样本复算，不做抽样估计。
//
// 用法：
//
//	./loadprobe -url http://127.0.0.1:8080/api/v2/explorer/overview -concurrency 16 -duration 30s \
//	            -cookie "session=...; csrf=..." -json out.json -label explorer-overview-lan
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type sample struct {
	Status int
	Millis float64
	Bytes  int
	Err    string
}

type report struct {
	Label       string         `json:"label"`
	URL         string         `json:"url"`
	Method      string         `json:"method"`
	Concurrency int            `json:"concurrency"`
	WallSeconds float64        `json:"wall_seconds"`
	Requests    int            `json:"requests"`
	RPS         float64        `json:"rps"`
	Errors      int            `json:"errors"`
	StatusCount map[string]int `json:"status_counts"`
	P50         float64        `json:"p50_ms"`
	P90         float64        `json:"p90_ms"`
	P95         float64        `json:"p95_ms"`
	P99         float64        `json:"p99_ms"`
	Min         float64        `json:"min_ms"`
	Max         float64        `json:"max_ms"`
	AvgMillis   float64        `json:"avg_ms"`
	AvgBytes    float64        `json:"avg_response_bytes"`
	ErrorSample []string       `json:"error_samples,omitempty"`
}

func main() {
	url := flag.String("url", "", "目标 URL")
	concurrency := flag.Int("concurrency", 8, "并发数")
	duration := flag.Duration("duration", 30*time.Second, "持续时间")
	timeout := flag.Duration("timeout", 30*time.Second, "单请求超时")
	cookie := flag.String("cookie", "", "Cookie 头")
	method := flag.String("method", "GET", "HTTP 方法")
	body := flag.String("body", "", "请求体（非 GET 时发送）")
	headers := flag.String("header", "", "额外头，形如 a=b;c=d")
	outPath := flag.String("json", "", "结果 JSON 输出路径")
	label := flag.String("label", "", "本次压测标签")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "缺少 -url")
		os.Exit(2)
	}

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 4,
			MaxIdleConnsPerHost: *concurrency * 4,
			MaxConnsPerHost:     *concurrency * 4,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var mu sync.Mutex
	samples := make([]sample, 0, 65536)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				var rdr io.Reader
				if *body != "" {
					rdr = strings.NewReader(*body)
				}
				req, err := http.NewRequestWithContext(ctx, *method, *url, rdr)
				if err != nil {
					return
				}
				if *cookie != "" {
					req.Header.Set("Cookie", *cookie)
				}
				if *body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				for _, kv := range strings.Split(*headers, ";") {
					if kv == "" {
						continue
					}
					parts := strings.SplitN(kv, "=", 2)
					if len(parts) == 2 {
						req.Header.Set(parts[0], parts[1])
					}
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				el := time.Since(t0).Seconds() * 1000
				s := sample{Millis: el}
				if err != nil {
					s.Err = err.Error()
				} else {
					n, _ := io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					s.Status = resp.StatusCode
					s.Bytes = int(n)
				}
				mu.Lock()
				samples = append(samples, s)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	wall := time.Since(start).Seconds()

	rep := summarize(*label, *url, *method, *concurrency, wall, samples)
	if *outPath != "" {
		if f, err := os.Create(*outPath); err == nil {
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
			_ = f.Close()
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
}

func summarize(label, url, method string, conc int, wall float64, samples []sample) report {
	r := report{Label: label, URL: url, Method: method, Concurrency: conc,
		WallSeconds: wall, Requests: len(samples), StatusCount: map[string]int{}}
	if len(samples) == 0 {
		return r
	}
	ms := make([]float64, 0, len(samples))
	total, totalBytes := 0.0, 0
	for _, s := range samples {
		ms = append(ms, s.Millis)
		total += s.Millis
		totalBytes += s.Bytes
		if s.Err != "" {
			r.Errors++
			if len(r.ErrorSample) < 5 {
				r.ErrorSample = append(r.ErrorSample, s.Err)
			}
			continue
		}
		r.StatusCount[fmt.Sprintf("%d", s.Status)]++
		if s.Status >= 400 {
			r.Errors++
		}
	}
	sort.Float64s(ms)
	pick := func(q float64) float64 {
		idx := int(q * float64(len(ms)-1))
		return ms[idx]
	}
	r.P50, r.P90, r.P95, r.P99 = pick(0.50), pick(0.90), pick(0.95), pick(0.99)
	r.Min, r.Max = ms[0], ms[len(ms)-1]
	r.AvgMillis = total / float64(len(samples))
	r.AvgBytes = float64(totalBytes) / float64(len(samples))
	if wall > 0 {
		r.RPS = float64(len(samples)) / wall
	}
	return r
}
