package meter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Server limits. TMM sends one flush batch per connection and closes right after.
const (
	ReadDeadline = 10 * time.Second
	// MaxConnBytes caps what one connection may send: a full flush batch with room to spare.
	MaxConnBytes = MaxLine * 256
	// MaxConns caps concurrent senders; more wait in the accept backlog.
	MaxConns = 64
)

// Server accepts usage lines over TCP.
//
// Anyone who can reach the listener could forge usage, and usage is written back onto Run:ai Projects, so
// two checks run before a byte is counted: the peer must be in AllowedPeers (checked before reading), and,
// when Token is set, the first line must be "auth <token>". The iRule sends that line first on every
// connection. The token is rendered into the F5BigCneIrule, so it raises the bar against forging from the
// network; RBAC on the f5-bnk namespace stays the real boundary.
type Server struct {
	Metrics *Metrics
	Log     *slog.Logger
	// AllowedPeers are the source prefixes (TMM self-IPs) allowed to send. Empty rejects every peer.
	AllowedPeers []netip.Prefix
	// Token, when set, must arrive as the connection's first line: "auth <token>".
	Token string
}

// ParsePeers reads a comma-separated list of IPs and CIDRs.
func ParsePeers(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("allowed peer %q: %w", f, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("allowed peer %q: %w", f, err)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// Allowed reports whether a peer address may send usage.
func (s *Server) Allowed(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	a, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, p := range s.AllowedPeers {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Serve accepts connections on l until ctx is done.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	sem := make(chan struct{}, MaxConns)
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			s.handle(c)
		}()
	}
}

func (s *Server) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	if !s.Allowed(c.RemoteAddr()) {
		s.Metrics.Rejected.Inc()
		s.warn("usage sender not in --allowed-peers, dropped", "peer", c.RemoteAddr().String())
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(ReadDeadline))
	n, err := s.Ingest(io.LimitReader(c, MaxConnBytes))
	if err != nil {
		s.warn("usage sender", "peer", c.RemoteAddr().String(), "records", n, "err", err)
	}
}

func (s *Server) warn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
	}
}

// ErrUnauthenticated means the connection did not open with the right "auth <token>" line.
var ErrUnauthenticated = errors.New("usage sender did not authenticate")

// authenticate consumes and checks the "auth <token>" preamble.
func (s *Server) authenticate(br *bufio.Reader) error {
	line, err := br.ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return ErrUnauthenticated
	}
	got, ok := strings.CutPrefix(strings.TrimSpace(string(line)), "auth ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
		return ErrUnauthenticated
	}
	return nil
}

// Ingest reads newline-delimited records from r until EOF and returns how many were counted. Lines over
// MaxLine and lines that do not parse are counted as malformed and skipped. A trailing line without a
// newline is parsed too (TMM may close right after the last byte).
// When Token is set the first line must be "auth <token>"; otherwise nothing is counted, the connection
// is rejected and ErrUnauthenticated returned.
func (s *Server) Ingest(r io.Reader) (int, error) {
	br := bufio.NewReaderSize(r, MaxLine+2)
	if s.Token != "" {
		if err := s.authenticate(br); err != nil {
			s.Metrics.Rejected.Inc()
			return 0, err
		}
	}
	counted := 0
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			s.Metrics.Malformed.Inc()
			// Discard the rest of the oversized line.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err != nil {
				return counted, eof(err)
			}
			continue
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if rec, perr := Parse(string(line)); perr != nil {
				s.Metrics.Malformed.Inc()
			} else {
				s.Metrics.Observe(rec)
				counted++
			}
		}
		if err != nil {
			return counted, eof(err)
		}
	}
}

func eof(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
