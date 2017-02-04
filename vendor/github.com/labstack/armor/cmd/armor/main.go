package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	slog "log"
	"net"
	"os"

	"github.com/labstack/armor"
	"github.com/labstack/armor/http"
	"github.com/labstack/gommon/color"
	"github.com/labstack/gommon/log"
)

const (

	// http://patorjk.com/software/taag/#p=display&f=Small%20Slant&t=Armor
	banner = `
   ___                     
  / _ | ______ _  ___  ____
 / __ |/ __/  ' \/ _ \/ __/
/_/ |_/_/ /_/_/_/\___/_/    %s

Uncomplicated, modern HTTP server
%s
________________________O/_______
                        O\
`
	defaultConfig = `{
    "address": ":8080",
    "plugins": [{
			"name": "logger"
		}, {
			"name": "static",
			"browse": true,
			"root": "."
		}]
  }`
)

func main() {
	// Initialize
	logger := log.New("armor")
	colorer := color.New()
	logger.SetLevel(log.INFO)
	colorer.SetOutput(logger.Output())
	slog.SetOutput(logger.Output())
	a := &armor.Armor{
		Logger:  logger,
		Colorer: colorer,
	}

	// Global flags
	c := flag.String("c", "", "config file")
	p := flag.String("p", "", "listen port")
	v := flag.Bool("v", false, "print the version")

	// daemon := flag.Bool("d", false, "run in daemon mode")
	// -daemon
	// -p [http port]
	// -w [--www]
	// -v [--version]
	// -h [--help]
	// --pid
	// Commands
	// - stop
	// - restart
	// - reload
	// port := flag.String("p", "", "the port to bind to")
	// directory?
	flag.Parse()

	if *v {
		color.Printf("armor %s\n", color.Red("v"+armor.Version))
		os.Exit(0)
	}

	// Load config
	data, err := ioutil.ReadFile(*c)
	if err != nil {
		// Use default config
		data = []byte(defaultConfig)
	}
	if err = json.Unmarshal(data, a); err != nil {
		if ute, ok := err.(*json.UnmarshalTypeError); ok {
			logger.Fatalf("error parsing configuration file, type=type-error, expected=%v, got=%v, offset=%v", ute.Type, ute.Value, ute.Offset)
		} else if se, ok := err.(*json.SyntaxError); ok {
			logger.Fatalf("error parsing configuration file, type=syntax-error, offset=%v, error=%v", se.Offset, se.Error())
		} else {
			logger.Fatalf("error parsing configuration file, error=%v", err)
		}
	}

	// Flags should override
	if *p != "" {
		a.Address = net.JoinHostPort("", *p)
	}

	// Defaults
	if a.Address == "" {
		a.Address = ":80"
	}

	// Initialize and load the plugins
	h := http.Init(a)
	h.LoadPlugins()

	// Start server - start
	colorer.Printf(banner, colorer.Red("v"+armor.Version), colorer.Blue(armor.Website))
	if a.TLS != nil {
		go func() {
			logger.Fatal(h.StartTLS())
		}()
	}
	logger.Fatal(h.Start())
	// Start server - end
}
