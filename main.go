package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"go.sia.tech/core/gateway"
	"go.sia.tech/coreutils"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/syncer"
	"go.sia.tech/walletd/v2/persist/sqlite"
	"go.sia.tech/walletd/v2/wallet"
	"go.uber.org/zap"
)

const version = "0.1.0"

var (
	//go:embed web web/assets
	webFiles embed.FS
)

type app struct {
	manifest        chain.QdayManifest
	cm              *chain.Manager
	wm              *wallet.Manager
	sy              *syncer.Syncer
	started         time.Time
	queries         chan struct{}
	static          http.Handler
	nodeStatusURL   string
	nodeHTTP        *http.Client
	nodeConnections atomic.Int64
	apiLimiter      *apiRateLimiter
	rich            *richListIndex
}

func loadManifest(path string) (m chain.QdayManifest, err error) {
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return m, errors.New("trailing manifest content")
	}
	return m, m.Validate()
}

func parsePeers(s string) ([]string, error) {
	seen := make(map[string]bool)
	var peers []string
	for _, peer := range strings.Split(s, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		host, port, err := net.SplitHostPort(peer)
		if err != nil || host == "" || port == "" {
			return nil, fmt.Errorf("invalid peer %q: expected host:port", peer)
		}
		if !seen[peer] {
			seen[peer] = true
			peers = append(peers, peer)
		}
	}
	if len(peers) == 0 {
		return nil, errors.New("at least one QDAY peer is required")
	}
	return peers, nil
}

func newApp(dataDir, manifestPath string, peers []string, nodeStatusURL string, logger *zap.Logger) (*app, func(), error) {
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load manifest: %w", err)
	} else if manifest.Development {
		return nil, nil, errors.New("the public explorer accepts QDAY mainnet only")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, nil, err
	} else if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, nil, err
	}

	bdb, err := coreutils.OpenBoltChainDB(filepath.Join(dataDir, "consensus.db"))
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = bdb.Close() }
	db, tip, err := chain.NewDBStore(bdb, &manifest.Network, manifest.Genesis, nil)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	cm := chain.NewManager(db, tip, chain.WithLog(logger.Named("chain")))
	if index, ok := cm.BestIndex(0); !ok || index.ID != manifest.Genesis.ID() {
		cleanup()
		return nil, nil, errors.New("explorer data belongs to another genesis")
	}

	store, err := sqlite.OpenDatabase(filepath.Join(dataDir, "index.sqlite3"), sqlite.WithLog(logger.Named("index")))
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, peer := range peers {
		if err := store.AddPeer(peer); err != nil {
			_ = store.Close()
			cleanup()
			return nil, nil, err
		}
	}
	peerStore, err := sqlite.NewPeerStore(store)
	if err != nil {
		_ = store.Close()
		cleanup()
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = store.Close()
		cleanup()
		return nil, nil, err
	}
	sy := syncer.New(listener, cm, peerStore, gateway.Header{
		GenesisID:  manifest.Genesis.ID(),
		UniqueID:   gateway.GenerateUniqueID(),
		NetAddress: listener.Addr().String(),
	},
		syncer.WithBootstrapPeers(peers),
		syncer.WithMaxInboundPeers(0),
		syncer.WithMaxOutboundPeers(4),
		syncer.WithLogger(logger.Named("p2p")),
	)
	go func() {
		if err := sy.Run(); err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Error("syncer stopped", zap.Error(err))
		}
	}()

	wm, err := wallet.NewManager(cm, store,
		wallet.WithLogger(logger.Named("wallet")),
		wallet.WithIndexMode(wallet.IndexModeFull),
	)
	if err != nil {
		_ = sy.Close()
		_ = listener.Close()
		_ = store.Close()
		cleanup()
		return nil, nil, err
	}
	rich, err := openRichListIndex(filepath.Join(dataDir, "index.sqlite3"))
	if err != nil {
		_ = wm.Close()
		_ = sy.Close()
		_ = listener.Close()
		_ = store.Close()
		cleanup()
		return nil, nil, fmt.Errorf("open rich-list index: %w", err)
	}
	staticRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		_ = rich.db.Close()
		_ = wm.Close()
		_ = sy.Close()
		_ = listener.Close()
		_ = store.Close()
		cleanup()
		return nil, nil, err
	}
	a := &app{
		manifest:      manifest,
		cm:            cm,
		wm:            wm,
		sy:            sy,
		started:       time.Now().UTC(),
		queries:       make(chan struct{}, 24),
		static:        http.FileServer(http.FS(staticRoot)),
		nodeStatusURL: nodeStatusURL,
		apiLimiter:    newAPIRateLimiter(),
		rich:          rich,
		nodeHTTP: &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: false,
			},
		},
	}
	return a, func() {
		_ = rich.db.Close()
		_ = wm.Close()
		_ = sy.Close()
		_ = listener.Close()
		_ = store.Close()
		cleanup()
	}, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func main() {
	manifestPath := flag.String("network", "qday-mainnet.json", "QDAY mainnet manifest")
	dataDir := flag.String("data", "explorer-data", "explorer chain and index directory")
	listenAddr := flag.String("listen", "127.0.0.1:8080", "public HTTP listen address")
	peerString := flag.String("peers", "seed1.pqday.com:19771,seed2.pqday.com:19771,seed3.pqday.com:19771", "comma-separated QDAY peers")
	nodeStatusURL := flag.String("node-status", "http://127.0.0.1:19770/api/network-status", "local QDAY node network-status URL")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("QDAY Explorer", version)
		return
	}
	peers, err := parsePeers(*peerString)
	if err != nil {
		log.Fatal(err)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}
	defer logger.Sync()
	a, closeApp, err := newApp(*dataDir, *manifestPath, peers, *nodeStatusURL, logger)
	if err != nil {
		logger.Fatal("explorer startup failed", zap.Error(err))
	}
	defer closeApp()

	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           securityHeaders(a.handler()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	logger.Info("QDAY explorer started",
		zap.String("version", version),
		zap.String("http", *listenAddr),
		zap.String("genesis", a.manifest.Genesis.ID().String()),
		zap.Strings("peers", peers),
		zap.String("nodeStatus", *nodeStatusURL),
	)
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("HTTP server stopped", zap.Error(err))
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
