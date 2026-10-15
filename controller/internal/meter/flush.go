package meter

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The iRule's flush contract (kept in step with internal/bnk; the meter does not import it to stay small).
const (
	flushPath   = "/bnk/flush"
	flushHeader = "X-BNK-Flush"
	flushBatch  = 100
	// A tenant with a full batch is asked again at once, at most this many times per pass.
	maxFlushRounds = 10
)

var gatewayGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayList"}

// Flusher asks every tenant Gateway's iRule to send its parked usage lines (GET /bnk/flush). The usage
// records reach the TCP listener; the HTTP answer only says how many left.
type Flusher struct {
	Client    client.Client
	Namespace string
	Token     string
	HTTP      *http.Client
	Log       *slog.Logger
}

// Targets lists "VIP:port" of every Gateway the tenancy controller manages.
func (f *Flusher) Targets(ctx context.Context) ([]string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gatewayGVK)
	if err := f.Client.List(ctx, list, client.InNamespace(f.Namespace),
		client.MatchingLabels{"app.kubernetes.io/managed-by": "bnk-runai-tenancy"}); err != nil {
		return nil, err
	}
	var out []string
	for _, gw := range list.Items {
		addrs, _, _ := unstructured.NestedSlice(gw.Object, "spec", "addresses")
		ls, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		if len(addrs) == 0 || len(ls) == 0 {
			continue
		}
		ip, _, _ := unstructured.NestedString(addrs[0].(map[string]any), "value")
		port, _, _ := unstructured.NestedInt64(ls[0].(map[string]any), "port")
		if ip != "" && port > 0 {
			out = append(out, net.JoinHostPort(ip, strconv.FormatInt(port, 10)))
		}
	}
	return out, nil
}

// Once flushes every tenant once.
func (f *Flusher) Once(ctx context.Context) {
	targets, err := f.Targets(ctx)
	if err != nil {
		f.Log.Warn("list tenant gateways", "err", err)
		return
	}
	for _, t := range targets {
		for round := 0; round < maxFlushRounds; round++ {
			n, err := f.flush(ctx, t)
			if err != nil {
				f.Log.Debug("flush", "target", t, "err", err)
				break
			}
			if n < flushBatch {
				break
			}
		}
	}
}

// Run flushes every interval until ctx is done.
func (f *Flusher) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		f.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (f *Flusher) flush(ctx context.Context, target string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+flushPath, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set(flushHeader, f.Token)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	n, _ := strconv.Atoi(resp.Header.Get("X-BNK-Flushed"))
	return n, nil
}
