package syncer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	defaultLANAddr = ":47822"
	discoveryPort  = 47823
	announceEvery  = 30 * time.Second
	peerTTL        = 2 * time.Minute
)

// LAN sync: each device with LAN enabled serves its snapshot and media over HTTP, guarded by the
// shared group code, and announces itself by UDP broadcast so peers need no addresses typed in.
// Traffic is plain HTTP: fine on a home network, not on one you do not trust.

type lanSource struct {
	base   string
	code   string
	client *http.Client
}

func (l *lanSource) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+l.code)
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()

		return nil, fmt.Errorf("%s: %s", path, resp.Status)
	}

	return resp, nil
}

func (l *lanSource) Snapshot(ctx context.Context) (*Snapshot, error) {
	resp, err := l.get(ctx, "/sync/snapshot")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	snap := &Snapshot{}
	if err := json.NewDecoder(resp.Body).Decode(snap); err != nil {
		return nil, err
	}

	return snap, nil
}

func (l *lanSource) Fetch(ctx context.Context, name string) (io.ReadCloser, error) {
	resp, err := l.get(ctx, "/sync/media/"+name)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// lanHandler serves only the snapshot and media; the library API stays on the loopback listener.
func (s *Syncer) lanHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			want := "Bearer " + s.Config().GroupCode
			if subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte(want)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/sync/snapshot", func(w http.ResponseWriter, req *http.Request) {
		snap, err := Export(req.Context(), s.DB, s.Config().DeviceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap)
	})
	r.Get("/sync/media/*", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "*")
		file, err := s.Media.Open(name)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, req)
			return
		}
		http.ServeContent(w, req, name, info.ModTime(), file)
	})

	return r
}

type peer struct {
	id   string
	host string
	port int
	seen time.Time
}

type announcement struct {
	App   string `json:"app"`
	ID    string `json:"id"`
	Port  int    `json:"port"`
	Group string `json:"group"`
}

// groupHash lets peers recognise each other without broadcasting the secret itself.
func groupHash(code string) string {
	sum := sha256.Sum256([]byte("utane-sync:" + code))

	return hex.EncodeToString(sum[:8])
}

func lanAddr() string {
	if addr := os.Getenv("UTANE_SYNC_ADDR"); addr != "" {
		return addr
	}

	return defaultLANAddr
}

type lanService struct {
	server *http.Server
	udp    net.PacketConn
	cancel context.CancelFunc
}

func (l *lanService) stop() {
	l.cancel()
	l.server.Close()
	if l.udp != nil {
		l.udp.Close()
	}
}

// applyLAN starts or stops the LAN listener and discovery to match the config.
func (s *Syncer) applyLAN() error {
	s.lanMu.Lock()
	defer s.lanMu.Unlock()

	want := s.Config().LAN
	if !want && s.lan != nil {
		s.lan.stop()
		s.lan = nil
	}
	if !want || s.lan != nil {
		return nil
	}

	ln, err := net.Listen("tcp", lanAddr())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := &lanService{
		server: &http.Server{Handler: s.lanHandler(), ReadHeaderTimeout: 10 * time.Second},
		cancel: cancel,
	}
	go svc.server.Serve(ln)

	port := ln.Addr().(*net.TCPAddr).Port
	if udp, err := net.ListenPacket("udp4", ":"+strconv.Itoa(discoveryPort)); err != nil {
		slog.Warn("sync discovery listener", "error", err)
	} else {
		svc.udp = udp
		go s.listenAnnouncements(ctx, udp)
	}
	go s.announce(ctx, port)
	s.lan = svc

	return nil
}

func (s *Syncer) announce(ctx context.Context, port int) {
	ticker := time.NewTicker(announceEvery)
	defer ticker.Stop()
	for {
		cfg := s.Config()
		msg, _ := json.Marshal(announcement{App: "utane", ID: cfg.DeviceID, Port: port, Group: groupHash(cfg.GroupCode)})
		if conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: discoveryPort}); err == nil {
			conn.Write(msg)
			conn.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Syncer) listenAnnouncements(ctx context.Context, udp net.PacketConn) {
	buf := make([]byte, 1024)
	for ctx.Err() == nil {
		n, from, err := udp.ReadFrom(buf)
		if err != nil {
			return
		}
		var msg announcement
		cfg := s.Config()
		if json.Unmarshal(buf[:n], &msg) != nil || msg.App != "utane" || msg.ID == cfg.DeviceID ||
			msg.Group != groupHash(cfg.GroupCode) || msg.Port <= 0 || msg.Port > 65535 {
			continue
		}
		host, _, err := net.SplitHostPort(from.String())
		if err != nil {
			continue
		}
		s.peersMu.Lock()
		_, known := s.peers[msg.ID]
		s.peers[msg.ID] = peer{id: msg.ID, host: host, port: msg.Port, seen: time.Now()}
		s.peersMu.Unlock()
		if !known {
			s.Trigger()
		}
	}
}

// livePeers returns the peers heard from recently.
func (s *Syncer) livePeers() []peer {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()

	var live []peer
	for id, p := range s.peers {
		if time.Since(p.seen) > peerTTL {
			delete(s.peers, id)
			continue
		}
		live = append(live, p)
	}

	return live
}

func (s *Syncer) pullPeers(ctx context.Context) (int, error) {
	changed := 0
	var problems []error
	code := s.Config().GroupCode
	for _, p := range s.livePeers() {
		src := &lanSource{
			base:   "http://" + net.JoinHostPort(p.host, strconv.Itoa(p.port)),
			code:   code,
			client: s.Client,
		}
		n, err := s.merger.Merge(ctx, src)
		changed += n
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", p.host, err))
		}
	}

	return changed, errors.Join(problems...)
}
