package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	IndexPath string `help:"Output Bleve index path." required:"true" arg:"" type:"path"`
	Input     string `help:"Input PBF file."          required:"true" arg:"" type:"path"`
}

func (b *BuildCmd) Run(conf *config.Config) error {
	indexPath := b.IndexPath
	if !strings.HasSuffix(indexPath, ".bleve") {
		indexPath += ".bleve"
	}

	slog.Info("building index", "path", indexPath, "input", b.Input)

	m := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(indexPath, m)
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
	indices      map[string]bleve.Index
	pbfPaths     map[string]string
	pmtilesPaths map[string]string
	indexLock    sync.RWMutex
	conf         *config.Config
	cache        *search.QueryCache
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.indexLock.RLock()
	defer s.indexLock.RUnlock()

	mux := http.NewServeMux()
	api.RegisterHandlersWithPBF(mux, api.HandlerOptions{
		Indices:      s.indices,
		PBFPaths:     s.pbfPaths,
		PMTilesPaths: s.pmtilesPaths,
		Conf:         s.conf,
		Cache:        s.cache,
	})

	// Serve static files from web directory if it exists
	if _, err := os.Stat("web"); err == nil {
		mux.Handle("/", http.FileServer(http.Dir("web")))
	}

	handler := api.CORSMiddleware(mux, s.conf.Server.AllowedOrigins)
	handler.ServeHTTP(w, r)
}

func (s *ServeCmd) initIndex(conf *config.Config) (map[string]bleve.Index, error) {
	indices := make(map[string]bleve.Index)
	for _, path := range conf.IndexPaths {
		if _, err := os.Stat(path); err == nil {
			idx, openErr := bleve.Open(path)
			if openErr != nil {
				return nil, fmt.Errorf("could not open index %s: %w", path, openErr)
			}
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			indices[name] = idx
		} else {
			slog.Warn("index path does not exist, skipping", "path", path)
		}
	}

	if len(indices) == 0 {
		indices = nil
	}

	return indices, nil
}

func (s *ServeCmd) validateSources(indices map[string]bleve.Index, pbfPaths, pmtilesPaths map[string]string) error {
	if len(indices) > 0 || len(pbfPaths) > 0 || len(pmtilesPaths) > 0 {
		return nil
	}

	return errors.New("no valid search sources found (indices, pbf_paths, or pmtiles_paths)")
}

//nolint:nilnil // returning nil cache is intended when disabled
func (s *ServeCmd) initQueryCache(conf *config.Config) (*search.QueryCache, error) {
	if !conf.CacheEnabled {
		return nil, nil
	}

	cacheSize := conf.CacheSize
	if cacheSize <= 0 {
		cacheSize = config.DefaultCacheSize
	}
	cacheTTL := conf.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = config.DefaultCacheTTL
	}

	c, err := search.NewQueryCache(cacheSize, cacheTTL)
	if err != nil {
		return nil, err
	}

	slog.Info("query cache initialized", "size", cacheSize, "ttl", cacheTTL)
	return c, nil
}

func (s *ServeCmd) Run(conf *config.Config) error {
	slog.Info(
		"serving",
		"host",
		conf.Server.Host,
		"port",
		conf.Server.Port,
		"indices",
		conf.IndexPaths,
		"pbfs",
		conf.PBFPaths,
		"pmtiles",
		conf.PMTilesPaths,
	)

	indices, err := s.initIndex(conf)
	if err != nil {
		return err
	}

	pbfPaths := make(map[string]string)
	for _, p := range conf.PBFPaths {
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		pbfPaths[name] = p
	}

	pmtilesPaths := make(map[string]string)
	for _, p := range conf.PMTilesPaths {
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		pmtilesPaths[name] = p
	}

	if validateErr := s.validateSources(indices, pbfPaths, pmtilesPaths); validateErr != nil {
		return validateErr
	}

	cache, err := s.initQueryCache(conf)
	if err != nil {
		slog.Warn("failed to create query cache, continuing without it", "error", err)
	}

	s.initCategoryMapper(conf)

	srv := &Server{
		indices:      indices,
		pbfPaths:     pbfPaths,
		pmtilesPaths: pmtilesPaths,
		conf:         conf,
		cache:        cache,
	}

	addr := fmt.Sprintf("%s:%d", conf.Server.Host, conf.Server.Port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv,
	}

	s.handleSignals(conf, srv, httpSrv)

	slog.Info("starting server", "addr", addr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}

	srv.indexLock.Lock()
	for _, idx := range srv.indices {
		idx.Close()
	}
	srv.indexLock.Unlock()

	return nil
}

func (s *ServeCmd) initCategoryMapper(conf *config.Config) {
	// Initialize CategoryMapper from ontology
	ont := osm.DefaultOntology()
	if conf.OntologyPath != "" {
		var ontErr error
		ont, ontErr = osm.LoadOntologyFromCSV(conf.OntologyPath)
		if ontErr != nil {
			slog.Warn("failed to load ontology, using default", "error", ontErr)
			ont = osm.DefaultOntology()
		}
	}
	search.CategoryMapper = func(q string) []search.CategoryMatch {
		q = strings.ToLower(q)
		matches := ont.GetTagsForLabel(q)
		if len(matches) == 0 && strings.HasSuffix(q, "s") {
			// Try singular if plural failed
			matches = ont.GetTagsForLabel(q[:len(q)-1])
		}
		if len(matches) == 0 {
			return nil
		}
		result := make([]search.CategoryMatch, len(matches))
		for i, m := range matches {
			result[i] = search.CategoryMatch{Key: m.Key, Value: m.Value}
		}
		return result
	}
}

func (s *ServeCmd) handleSignals(conf *config.Config, srv *Server, httpSrv *http.Server) {
	// Handle SIGHUP for hot-reload
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGHUP:
				slog.Info("received SIGHUP, reloading indices")
				newIndices, err := s.initIndex(conf)
				if err != nil {
					slog.Error("failed to reload indices", "error", err)
					continue
				}

				srv.indexLock.Lock()
				oldIndices := srv.indices
				srv.indices = newIndices

				// Update PBF and PMTiles paths too
				pbfPaths := make(map[string]string)
				for _, p := range conf.PBFPaths {
					name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
					pbfPaths[name] = p
				}
				pmtilesPaths := make(map[string]string)
				for _, p := range conf.PMTilesPaths {
					name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
					pmtilesPaths[name] = p
				}
				srv.pbfPaths = pbfPaths
				srv.pmtilesPaths = pmtilesPaths

				// Clear cache on index reload
				if srv.cache != nil {
					srv.cache.Clear()
					slog.Info("query cache cleared on index reload")
				}
				srv.indexLock.Unlock()

				for _, idx := range oldIndices {
					idx.Close()
				}
				slog.Info("indices reloaded")
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("shutting down")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = httpSrv.Shutdown(ctx)
				return
			}
		}
	}()
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

	conf := config.Config{
		OnlyNamed:        true,
		DisableAltNames:  true,
		DisableKeyValues: true,
	}
	if parseErr := toml.Unmarshal(confData, &conf); parseErr != nil {
		slog.Error("failed to parse config file", "error", parseErr)
		os.Exit(1)
	}

	if runErr := ctx.Run(&conf); runErr != nil {
		slog.Error("command failed", "error", runErr)
		os.Exit(1)
	}
}
