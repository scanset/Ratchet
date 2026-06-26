// Package ollama is the minimal Ollama client: generate (optionally grammar-constrained), streaming
// generate, embed, and tags, plus the process-wide token meter and a cancel handle. Port of
// src.bak/Runtime/Ollama.cs.
//
// The C# port hand-deviated to use the platform HTTP stack (HttpWebRequest) instead of raw sockets;
// the Go equivalent is net/http, which handles chunked transfer and streaming for us. Two DEVLOG
// lessons are preserved: a read/stall timeout (a stalled Ollama errors instead of hanging forever)
// and the grammar-constrained `format` field (constrain the proposer's shape so the oracle has less
// to reject).
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/scanset/Ratchet/internal/jsonx"
)

const numCtx = 8192 // Ollama's default 2048 silently truncates long prompts.

// Cancel is a cancellation handle for an in-flight request, backed by a context. The GUI/console
// Cancel button calls Abort(), which cancels the request so a blocking call returns at once.
type Cancel struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewCancel makes a fresh cancel handle.
func NewCancel() *Cancel {
	ctx, cancel := context.WithCancel(context.Background())
	return &Cancel{ctx: ctx, cancel: cancel}
}

// Abort cancels the in-flight request (best effort, safe on a nil handle).
func (c *Cancel) Abort() {
	if c != nil && c.cancel != nil {
		c.cancel()
	}
}

func (c *Cancel) context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *Cancel) cancelled() bool {
	return c != nil && c.ctx != nil && c.ctx.Err() != nil
}

// TokenMeter is a process-wide tally of local-model token usage, so the console can show the operator
// how much work the LOCAL model did (and therefore did NOT cost in frontier tokens).
type tokenMeter struct {
	mu     sync.Mutex
	prompt int64
	eval   int64
	calls  int
}

var meter tokenMeter

// MeterRecord adds one call's prompt/eval token counts.
func MeterRecord(prompt, eval int64) {
	meter.mu.Lock()
	meter.prompt += prompt
	meter.eval += eval
	meter.calls++
	meter.mu.Unlock()
}

// MeterPrompt / MeterEval / MeterCalls / MeterTotal expose the running tallies.
func MeterPrompt() int64 { meter.mu.Lock(); defer meter.mu.Unlock(); return meter.prompt }
func MeterEval() int64   { meter.mu.Lock(); defer meter.mu.Unlock(); return meter.eval }
func MeterCalls() int    { meter.mu.Lock(); defer meter.mu.Unlock(); return meter.calls }
func MeterTotal() int64  { meter.mu.Lock(); defer meter.mu.Unlock(); return meter.prompt + meter.eval }

func longOf(o map[string]any, key string) int64 {
	if v, ok := jsonx.GetNumber(o, key); ok {
		return int64(v)
	}
	return 0
}

// send performs one request and returns the full response body as a string. timeout is the whole-
// request deadline; the cancel handle aborts it early.
func send(url, method, path, body string, timeout time.Duration, c *Cancel) (string, error) {
	full := strings.TrimRight(url, "/") + path
	ctx, cancel := context.WithTimeout(c.context(), timeout)
	defer cancel()

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, full, bodyReader)
	if err != nil {
		return "", fmt.Errorf("bad Ollama url '%s': %v", url, err)
	}
	req.Close = true // mirror the C# Connection: close
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := noProxyClient.Do(req)
	if err != nil {
		if c.cancelled() {
			return "", fmt.Errorf("request cancelled")
		}
		return "", fmt.Errorf("contacting Ollama at %s (timeout %s?): %v", full, timeout, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama at %s returned %d: %s", full, resp.StatusCode, string(data))
	}
	return string(data), nil
}

// a shared client with proxy auto-detection off (localhost; skip the latency).
var noProxyClient = &http.Client{
	Transport: &http.Transport{Proxy: nil},
}

// Generate makes one /api/generate call. With format set, the output is grammar-constrained to that
// JSON schema. Returns the trimmed `response` string.
func Generate(url, model, prompt string, format any, temperature float64, timeoutMs int, c *Cancel) (string, error) {
	options := map[string]any{"num_ctx": numCtx, "temperature": temperature}
	body := map[string]any{"model": model, "prompt": prompt, "stream": false, "options": options}
	if format != nil {
		body["format"] = format
	}
	raw, err := send(url, "POST", "/api/generate", jsonx.Serialize(body), time.Duration(timeoutMs)*time.Millisecond, c)
	if err != nil {
		return "", err
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing Ollama JSON: %v", err)
	}
	o := jsonx.AsObject(parsed)
	response, ok := jsonx.GetString(o, "response")
	if !ok {
		return "", fmt.Errorf("no 'response' field in Ollama reply: %s", raw)
	}
	MeterRecord(longOf(o, "prompt_eval_count"), longOf(o, "eval_count"))
	return strings.TrimSpace(response), nil
}

// GenerateStream streams /api/generate (freeform only - no format). It reads the NDJSON token
// stream, calls onToken for each piece as it arrives, and returns the full text. A per-read stall
// timeout (reset on every token) errors instead of hanging when Ollama goes quiet mid-stream.
func GenerateStream(url, model, prompt string, temperature float64, timeoutMs int, onToken func(string), c *Cancel) (string, error) {
	options := map[string]any{"num_ctx": numCtx, "temperature": temperature}
	body := map[string]any{"model": model, "prompt": prompt, "stream": true, "options": options}
	full := strings.TrimRight(url, "/") + "/api/generate"

	// A context cancelled by a stall timer that we reset on each token (the ReadWriteTimeout lesson).
	ctx, cancel := context.WithCancel(c.context())
	defer cancel()
	stall := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, "POST", full, bytes.NewReader([]byte(jsonx.Serialize(body))))
	if err != nil {
		return "", fmt.Errorf("bad Ollama url '%s': %v", url, err)
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")

	resp, err := noProxyClient.Do(req)
	if err != nil {
		if c.cancelled() {
			return "", fmt.Errorf("request cancelled")
		}
		return "", fmt.Errorf("contacting Ollama at %s (timeout %dms?): %v", full, timeoutMs, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama at %s returned %d: %s", full, resp.StatusCode, string(data))
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // NDJSON lines can be long
	for scanner.Scan() {
		stall.Reset(time.Duration(timeoutMs) * time.Millisecond)
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parsed, err := jsonx.Parse(line)
		if err != nil {
			continue
		}
		obj := jsonx.AsObject(parsed)
		if obj == nil {
			continue
		}
		if piece, ok := jsonx.GetString(obj, "response"); ok && piece != "" {
			sb.WriteString(piece)
			if onToken != nil {
				onToken(piece)
			}
		}
		if jsonx.GetBool(obj, "done", false) {
			MeterRecord(longOf(obj, "prompt_eval_count"), longOf(obj, "eval_count"))
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if c.cancelled() {
			return "", fmt.Errorf("request cancelled")
		}
		return "", fmt.Errorf("reading Ollama stream: %v", err)
	}
	return strings.TrimSpace(sb.String()), nil
}

// GenerateJSON is the schema-constrained convenience: parse the response string as a JSON object.
func GenerateJSON(url, model, prompt string, schema any, temperature float64, timeoutMs int, c *Cancel) (map[string]any, error) {
	text, err := Generate(url, model, prompt, schema, temperature, timeoutMs, c)
	if err != nil {
		return nil, err
	}
	parsed, err := jsonx.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("model returned non-JSON under schema: %v\n%s", err, text)
	}
	obj := jsonx.AsObject(parsed)
	if obj == nil {
		return nil, fmt.Errorf("model returned non-JSON under schema:\n%s", text)
	}
	return obj, nil
}

// Tags lists installed models (/api/tags); also the cheapest reachability check.
func Tags(url string) ([]string, error) {
	raw, err := send(url, "GET", "/api/tags", "", 5*time.Second, nil)
	if err != nil {
		return nil, err
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing /api/tags JSON: %v", err)
	}
	var names []string
	for _, m := range jsonx.GetArr(jsonx.AsObject(parsed), "models") {
		if name, ok := jsonx.GetString(jsonx.AsObject(m), "name"); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// Embed makes one /api/embed call: returns the embedding vector for text (the embedder seat).
func Embed(url, model, text string, c *Cancel) ([]float64, error) {
	body := map[string]any{"model": model, "input": text}
	raw, err := send(url, "POST", "/api/embed", jsonx.Serialize(body), 30*time.Second, c)
	if err != nil {
		return nil, err
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing /api/embed JSON: %v", err)
	}
	obj := jsonx.AsObject(parsed)
	if obj == nil {
		return nil, fmt.Errorf("no JSON from /api/embed")
	}
	embs := jsonx.GetArr(obj, "embeddings") // {"embeddings":[[...]]}
	if len(embs) > 0 {
		return toFloats(jsonx.AsArr(embs[0])), nil
	}
	if single, ok := obj["embedding"]; ok { // older shape
		return toFloats(jsonx.AsArr(single)), nil
	}
	return nil, fmt.Errorf("no 'embeddings' field in /api/embed reply")
}

func toFloats(nums []any) []float64 {
	out := make([]float64, len(nums))
	for i, n := range nums {
		if d, ok := jsonx.ToDouble(n); ok {
			out[i] = d
		}
	}
	return out
}
