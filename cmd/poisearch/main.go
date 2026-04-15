package main

import (
	"context"
	"errors"
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

//nolint:nilnil // returning nil index is intended when path is empty
func (s *ServeCmd) initIndex(conf *config.Config) (bleve.Index, error) {
	if conf.IndexPath == "" {
		return nil, nil
	}

	if _, err := os.Stat(conf.IndexPath); err == nil {
		idx, openErr := bleve.Open(conf.IndexPath)
		if openErr != nil {
			return nil, fmt.Errorf("could not open index: %w", openErr)
		}
		return idx, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("error checking index path: %w", err)
	}

	slog.Warn("index path does not exist, skipping index load", "path", conf.IndexPath)
	return nil, nil
}

func (s *ServeCmd) validateSources(index bleve.Index, conf *config.Config) error {
	if index != nil {
		return nil
	}

	pbfExists := false
	if conf.PBFPath != "" {
		if _, err := os.Stat(conf.PBFPath); err == nil {
			pbfExists = true
		}
	}
	pmtilesExists := false
	if conf.PMTilesPath != "" {
		if _, err := os.Stat(conf.PMTilesPath); err == nil {
			pmtilesExists = true
		}
	}

	if !pbfExists && !pmtilesExists {
		return errors.New("could not open index and neither pbf_path nor pmtiles_path exist")
	}
	return nil
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
		"index",
		conf.IndexPath,
		"pbf",
		conf.PBFPath,
		"pmtiles",
		conf.PMTilesPath,
	)

	idx, err := s.initIndex(conf)
	if err != nil {
		return err
	}

	if validateErr := s.validateSources(idx, conf); validateErr != nil {
		return validateErr
	}

	cache, err := s.initQueryCache(conf)
	if err != nil {
		slog.Warn("failed to create query cache, continuing without it", "error", err)
	}

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

	srv := &Server{
		index:       idx,
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
				if conf.IndexPath == "" {
					slog.Warn("skipping reload because index_path is empty")
					continue
				}
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
				if oldIndex != nil {
					oldIndex.Close()
				}
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
	if srv.index != nil {
		srv.index.Close()
	}
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
