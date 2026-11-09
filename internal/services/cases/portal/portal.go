// Package portal implements the bounded RPC experiment described in docs/adr/0002_choose-go.md.
package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Result struct {
	Stage    string `json:"stage"`
	Requests int    `json:"requests"`
}
type state struct {
	props        map[float64]map[string]any
	children     map[float64][]float64
	sync, client any
	token        string
}

func object(b []byte, marker string) (map[string]any, error) {
	i := bytes.Index(b, []byte(marker))
	if i < 0 {
		return nil, errors.New("bootstrap marker missing")
	}
	var v map[string]any
	err := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b[i+len(marker):]))).Decode(&v)
	return v, err
}

func (s *state) apply(v map[string]any) error {
	sync, sok := v["syncId"].(float64)
	client, cok := v["clientId"].(float64)
	if !sok || !cok || !integer(sync) || !integer(client) {
		return errors.New("invalid RPC synchronization state")
	}
	s.sync, s.client = sync, client
	if t, ok := v["Vaadin-Security-Key"].(string); ok {
		s.token = t
	}
	rawChanges, exists := v["changes"]
	if !exists {
		return nil
	}
	cs, ok := rawChanges.([]any)
	if !ok {
		return errors.New("invalid RPC changes")
	}
	for _, raw := range cs {
		c, ok := raw.(map[string]any)
		if !ok {
			return errors.New("invalid RPC change")
		}
		n, ok := c["node"].(float64)
		if !ok || !integer(n) {
			return errors.New("invalid RPC node")
		}
		if s.props[n] == nil {
			s.props[n] = map[string]any{}
		}
		feat, _ := c["feat"].(float64)
		switch c["type"] {
		case "attach":
		case "put":
			k, ok := c["key"].(string)
			if !ok {
				return errors.New("invalid property key")
			}
			// Only semantic properties participate in discovery; styles and listeners must not overwrite them.
			if feat == 0 || feat == 1 || feat == 7 {
				s.props[n][k] = c["value"]
			}
		case "remove":
			if feat == 0 || feat == 1 || feat == 7 {
				k, _ := c["key"].(string)
				delete(s.props[n], k)
			}
		case "detach":
			delete(s.props, n)
			delete(s.children, n)
		case "clear", "empty":
			if feat == 2 {
				delete(s.children, n)
			}
		case "splice":
			if feat != 2 {
				continue
			}
			old := s.children[n]
			i, ok := c["index"].(float64)
			if !ok || !integer(i) {
				return errors.New("invalid child index")
			}
			r, _ := c["remove"].(float64)
			if !integer(r) || i > float64(len(old)) || i+r > float64(len(old)) {
				return errors.New("invalid child splice")
			}
			next := append([]float64{}, old[:int(i)]...)
			if raw, exists := c["addNodes"]; exists {
				added, ok := raw.([]any)
				if !ok {
					return errors.New("invalid child nodes")
				}
				for _, x := range added {
					f, ok := x.(float64)
					if !ok || !integer(f) {
						return errors.New("invalid child node")
					}
					next = append(next, f)
				}
			}
			s.children[n] = append(next, old[int(i+r):]...)
		default:
			return errors.New("unsupported RPC change")
		}
	}
	return nil
}

func (s *state) find(tag, label string) (float64, error) {
	var found []float64
	for n, p := range s.props {
		if p["tag"] == tag && (label == "" || p["label"] == label) {
			if tag == "vaadin-button" {
				matches := false
				for _, child := range s.children[n] {
					if s.props[child]["text"] == "Log in" {
						matches = true
					}
				}
				if !matches {
					continue
				}
			}
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		return 0, errors.New("missing or ambiguous login component")
	}
	return found[0], nil
}

func Probe(ctx context.Context, base, number, password string) (Result, error) {
	return probe(ctx, base, number, password, 2*time.Second, 6)
}

func probe(ctx context.Context, base, number, password string, interval time.Duration, budget int) (Result, error) {
	c := NewClient(Options{BaseURL: base})
	c.interval, c.budget = interval, budget
	return c.run(ctx, number, password)
}

func (c *Client) run(ctx context.Context, number, password string) (result Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.overall)
	defer cancel()
	defer func() { err = classify(err) }()
	jar, _ := cookiejar.New(nil)
	transport := c.transport
	var owned *http.Transport
	if transport == nil {
		owned = http.DefaultTransport.(*http.Transport).Clone()
		owned.DisableKeepAlives = true
		transport = owned
		defer owned.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Jar: jar, Timeout: c.perRequest, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var last time.Time
	request := func(method, path string, body any) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result.Requests >= c.budget {
			return nil, errors.New("request budget exhausted")
		}
		if !last.IsZero() {
			if err := c.wait(ctx, last.Add(c.interval).Sub(c.clock.Now())); err != nil {
				return nil, err
			}
		}
		var buf bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				return nil, err
			}
		}
		requestCtx, requestCancel := context.WithTimeout(ctx, c.perRequest)
		defer requestCancel()
		// A non-replayable body and fresh production connections prevent net/http
		// from replaying even the initial GET on a previously used connection.
		req, err := http.NewRequestWithContext(requestCtx, method, c.base+path, io.NopCloser(&buf))
		if err != nil {
			return nil, &Error{Kind: Protocol}
		}
		req.GetBody = nil
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		result.Requests++
		last = c.clock.Now()
		resp, err := client.Do(req)
		if err != nil {
			if requestCtx.Err() != nil {
				return nil, requestCtx.Err()
			}
			return nil, &Error{Kind: Transport}
		}
		defer resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, &Error{Kind: Authentication}
		}
		if resp.StatusCode != 200 {
			return nil, &Error{Kind: Transport}
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		if err != nil {
			if requestCtx.Err() != nil {
				return nil, requestCtx.Err()
			}
			return nil, &Error{Kind: Transport}
		}
		if len(b) > 4<<20 {
			return nil, errors.New("portal response exceeds limit")
		}
		return b, nil
	}
	b, err := request("GET", "/", nil)
	if err != nil {
		return result, err
	}
	uidl, err := object(b, "var uidl =")
	if err != nil {
		return result, err
	}
	config, err := object(b, "var config =")
	if err != nil {
		return result, err
	}
	if id, ok := config["v-uiId"].(float64); !ok || !integer(id) {
		return result, errors.New("missing UI identifier")
	}
	s := state{props: map[float64]map[string]any{}, children: map[float64][]float64{}}
	if err := s.apply(uidl); err != nil {
		return result, err
	}
	num, err := s.find("vaadin-text-field", "Number of application")
	if err != nil {
		return result, err
	}
	pass, err := s.find("vaadin-password-field", "Password")
	if err != nil {
		return result, err
	}
	button, err := s.find("vaadin-button", "")
	if err != nil {
		return result, err
	}
	if s.token == "" {
		return result, errors.New("missing security token")
	}
	event := func(n float64, name string) map[string]any {
		data := map[string]any{}
		if name == "click" {
			for _, k := range []string{"shiftKey", "metaKey", "ctrlKey", "altKey"} {
				data["event."+k] = false
			}
			for _, k := range []string{"detail", "clientX", "clientY", "button", "screenX", "screenY"} {
				data["event."+k] = 0
			}
		}
		return map[string]any{"type": "event", "node": n, "event": name, "data": data}
	}
	change := func(n float64, value, name string) []any {
		return []any{map[string]any{"type": "mSync", "node": n, "feature": 1, "property": "value", "value": value}, event(n, name)}
	}
	for step := 0; step < 4; step++ {
		// Each reply may replace login components.
		num, err = s.find("vaadin-text-field", "Number of application")
		if err != nil {
			return result, err
		}
		pass, err = s.find("vaadin-password-field", "Password")
		if err != nil {
			return result, err
		}
		button, err = s.find("vaadin-button", "")
		if err != nil {
			return result, err
		}
		rpc := [][]any{{event(pass, "value-changed")}, change(num, number, "change"), change(pass, password, "value-changed"), {event(button, "click")}}[step]
		path := "/?v-r=uidl&v-uiId=" + url.QueryEscape(fmt.Sprint(config["v-uiId"]))
		b, err = request("POST", path, map[string]any{"csrfToken": s.token, "syncId": s.sync, "clientId": s.client, "rpc": rpc})
		if err != nil {
			return result, err
		}
		var messages []map[string]any
		if err = json.Unmarshal(bytes.TrimPrefix(b, []byte("for(;;);")), &messages); err != nil || len(messages) == 0 {
			return result, errors.New("invalid RPC response")
		}
		for _, m := range messages {
			if err := s.apply(m); err != nil {
				return result, err
			}
		}
	}
	// Locate the field in the same row as the stage caption.
	var contains func(float64, string, map[float64]bool) bool
	contains = func(n float64, text string, seen map[float64]bool) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		p := s.props[n]
		if p["text"] == text || p["nodeValue"] == text {
			return true
		}
		for _, ch := range s.children[n] {
			if contains(ch, text, seen) {
				return true
			}
		}
		return false
	}
	for n := range s.children {
		if !contains(n, "Stage of your case", map[float64]bool{}) {
			continue
		}
		var values []string
		var walk func(float64, map[float64]bool)
		walk = func(id float64, seen map[float64]bool) {
			if seen[id] {
				return
			}
			seen[id] = true
			p := s.props[id]
			if p["tag"] == "vaadin-text-field" {
				if v, ok := p["value"].(string); ok && strings.TrimSpace(v) != "" {
					values = append(values, v)
				}
			}
			for _, ch := range s.children[id] {
				walk(ch, seen)
			}
		}
		walk(n, map[float64]bool{})
		if len(values) == 1 {
			result.Stage = values[0]
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			return result, nil
		}
	}
	if _, err := s.find("vaadin-password-field", "Password"); err == nil {
		return result, &Error{Kind: Authentication}
	}
	return result, &Error{Kind: Protocol}
}

func integer(v float64) bool {
	return v >= 0 && v <= 1<<53-1 && math.Trunc(v) == v
}
