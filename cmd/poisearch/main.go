package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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
	fmt.Printf("Building index at %s from %s\n", conf.IndexPath, b.Input)

	m := search.BuildIndexMapping(conf.Languages, conf.GeometryMode)
	index, err := search.OpenOrCreateIndex(conf.IndexPath, m)
	if err != nil {
		return err
	}
	defer index.Close()

	return osm.BuildIndex(b.Input, conf, index)
}

type ServeCmd struct{}

func (s *ServeCmd) Run(conf *config.Config) error {
	fmt.Printf("Serving at %s:%d from %s\n", conf.Server.Host, conf.Server.Port, conf.IndexPath)

	index, err := bleve.Open(conf.IndexPath)
	if err != nil {
		return fmt.Errorf("could not open index: %v", err)
	}
	defer index.Close()

	mux := http.NewServeMux()
	api.RegisterHandlers(mux, index, conf)

	addr := fmt.Sprintf("%s:%d", conf.Server.Host, conf.Server.Port)
	log.Printf("Starting server on %s", addr)
	return http.ListenAndServe(addr, mux)
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

	// Manually load TOML into the config struct since the user wants it required and no defaults.
	// This avoids any issues with kong-toml defaults or mapping.
	confData, err := os.ReadFile(cli.ConfigPath)
	if err != nil {
		log.Fatalf("failed to read config file: %v", err)
	}

	var conf config.Config
	if err := toml.Unmarshal(confData, &conf); err != nil {
		log.Fatalf("failed to parse config file: %v", err)
	}

	err = ctx.Run(&conf)
	if err != nil {
		log.Fatal(err)
	}
}
