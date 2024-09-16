package cmd

import (
	"os"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var log logr.Logger
var debug bool

var rootCmd = &cobra.Command{
	Use:               "kw",
	Short:             "KubeWire allows easy, direct connections to, and through, a Kubernetes cluster.",
	SilenceUsage:      true,
	DisableAutoGenTag: true,
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		config := zap.Config{
			Level:             zap.NewAtomicLevelAt(zap.InfoLevel),
			Development:       false,
			DisableCaller:     true,
			DisableStacktrace: false,
			Encoding:          "console",
			EncoderConfig:     zap.NewDevelopmentEncoderConfig(),
			OutputPaths:       []string{"stderr"},
			ErrorOutputPaths:  []string{"stderr"},
		}

		if debug {
			config.Development = true
			config.DisableCaller = false
			config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		}

		log = zapr.NewLogger(zap.Must(config.Build()))
	},
}

func init() {
	var (
		debugDefault bool
		err          error
	)

	if envDebug := os.Getenv("DEBUG"); envDebug != "" {
		debugDefault, err = strconv.ParseBool(envDebug)
		if err != nil {
			log.Error(err, "unable to parse DEBUG env variable")
		}
	}

	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", debugDefault, "Toggle debug logging")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
