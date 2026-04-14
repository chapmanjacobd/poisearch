package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	bleve "github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/api"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/pelletier/go-toml/v2"
)

type BuildCmd struct {
	Input string `help:"Input PBF file." required:"true" arg:"" type:"path"`
}

func (b *BuildCmd) Run(conf *config.Config) error {
	slog.Info("building index", "path", conf.IndexPath, "input", b.Input)

	m := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(conf.IndexPath, m)
	if err != nil {
		return err
	}
	defer index.Close()

	return osm.BuildIndex(b.Input, conf, index)
}

type ServeCmd struct{}

//nolint:gochecknoglobals // CLI struct required by kong
var cli struct {
	ConfigPath string   `help:"Path to config file."      required:"true"        name:"config" type:"path"`
	Build      BuildCmd `help:"Build the POI index."                      cmd:""`
	Serve      ServeCmd `help:"Serve the POI search API."                 cmd:""`
}

type Server struct {
	index       bleve.Index
	indexLock   sync.RWMutex
	conf        *config.Config
	pbfPath     string
	pmtilesPath string
	cache       *search.QueryCache
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.indexLock.RLock()
	defer s.indexLock.RUnlock()

	mux := http.NewServeMux()
	api.RegisterHandlersWithPBF(mux, api.HandlerOptions{
		Index:       s.index,
		Conf:        s.conf,
		PBFPath:     s.pbfPath,
		PMTilesPath: s.pmtilesPath,
		Cache:       s.cache,
	})

	handler := api.CORSMiddleware(mux, s.conf.Server.AllowedOrigins)
	handler.ServeHTTP(w, r)
}

func (s *ServeCmd) Run(conf *config.Config) error {
	slog.Info(
		"serving",
		"host",
		conf.Server.Host,
		"port",
		conf.Server.Port,
		"index",
		conf.IndexPath,
		"pbf",
		conf.PBFPath,
		"pmtiles",
		conf.PMTilesPath,
	)

	index, err := bleve.Open(conf.IndexPath)
	if err != nil {
		return fmt.Errorf("could not open index: %w", err)
	}

	// Initialize query cache if enabled
	var cache *search.QueryCache
	if conf.CacheEnabled {
		cacheSize := conf.CacheSize
		if cacheSize <= 0 {
			cacheSize = config.DefaultCacheSize
		}
		cacheTTL := conf.CacheTTL
		if cacheTTL <= 0 {
			cacheTTL = config.DefaultCacheTTL
		}

		cache, err = search.NewQueryCache(cacheSize, cacheTTL)
		if err != nil {
			slog.Warn("failed to create query cache, continuing without it", "error", err)
			cache = nil
		} else {
			slog.Info("query cache initialized", "size", cacheSize, "ttl", cacheTTL)
		}
	}

	srv := &Server{
		index:       index,
		conf:        conf,
		pbfPath:     conf.PBFPath,
		pmtilesPath: conf.PMTilesPath,
		cache:       cache,
	}

	addr := fmt.Sprintf("%s:%d", conf.Server.Host, conf.Server.Port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	// Handle SIGHUP for hot-reload
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGHUP:
				slog.Info("received SIGHUP, reloading index")
				newIndex, err := bleve.Open(conf.IndexPath)
				if err != nil {
					slog.Error("failed to reload index", "error", err)
					continue
				}
				srv.indexLock.Lock()
				oldIndex := srv.index
				srv.index = newIndex
				// Clear cache on index reload
				if srv.cache != nil {
					srv.cache.Clear()
					slog.Info("query cache cleared on index reload")
				}
				srv.indexLock.Unlock()
				oldIndex.Close()
				slog.Info("index reloaded")
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("shutting down")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = httpSrv.Shutdown(ctx)
				return
			}
		}
	}()

	slog.Info("starting server", "addr", addr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}

	srv.indexLock.Lock()
	srv.index.Close()
	srv.indexLock.Unlock()

	return nil
}

func main() {
	ctx := kong.Parse(&cli,
		kong.Name("poisearch"),
		kong.Description("Lightweight POI search."),
		kong.UsageOnError(),
	)

	confData, readErr := os.ReadFile(cli.ConfigPath)
	if readErr != nil {
		slog.Error("failed to read config file", "error", readErr)
		os.Exit(1)
	}

	var conf config.Config
	if parseErr := toml.Unmarshal(confData, &conf); parseErr != nil {
		slog.Error("failed to parse config file", "error", parseErr)
		os.Exit(1)
	}

	if runErr := ctx.Run(&conf); runErr != nil {
		slog.Error("command failed", "error", runErr)
		os.Exit(1)
	}
}
