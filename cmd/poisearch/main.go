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
	Input string `arg:"" help:"Input PBF file." required:"" type:"path"`
}

func (b *BuildCmd) Run(conf *config.Config) error {
	slog.Info("building index", "path", conf.IndexPath, "input", b.Input)

	m := search.BuildIndexMapping(conf.Languages, conf.GeometryMode)
	index, err := search.OpenOrCreateIndex(conf.IndexPath, m)
	if err != nil {
		return err
	}
	defer index.Close()

	return osm.BuildIndex(b.Input, conf, index)
}

type ServeCmd struct{}

type Server struct {
	index     bleve.Index
	indexLock sync.RWMutex
	conf      *config.Config
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.indexLock.RLock()
	defer s.indexLock.RUnlock()

	mux := http.NewServeMux()
	api.RegisterHandlers(mux, s.index, s.conf)
	mux.ServeHTTP(w, r)
}

func (s *ServeCmd) Run(conf *config.Config) error {
	slog.Info("serving", "host", conf.Server.Host, "port", conf.Server.Port, "index", conf.IndexPath)

	index, err := bleve.Open(conf.IndexPath)
	if err != nil {
		return fmt.Errorf("could not open index: %v", err)
	}

	srv := &Server{
		index: index,
		conf:  conf,
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
				srv.indexLock.Unlock()
				oldIndex.Close()
				slog.Info("index reloaded")
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("shutting down")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				httpSrv.Shutdown(ctx)
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

var cli struct {
	ConfigPath string   `help:"Path to config file." required:"" type:"path" name:"config"`
	Build      BuildCmd `cmd:"" help:"Build the POI index."`
	Serve      ServeCmd `cmd:"" help:"Serve the POI search API."`
}

func main() {
	ctx := kong.Parse(&cli,
		kong.Name("poisearch"),
		kong.Description("Lightweight POI search."),
		kong.UsageOnError(),
	)

	confData, err := os.ReadFile(cli.ConfigPath)
	if err != nil {
		slog.Error("failed to read config file", "error", err)
		os.Exit(1)
	}

	var conf config.Config
	if err := toml.Unmarshal(confData, &conf); err != nil {
		slog.Error("failed to parse config file", "error", err)
		os.Exit(1)
	}

	err = ctx.Run(&conf)
	if err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
