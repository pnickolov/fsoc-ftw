// Copyright 2022 Cisco Systems, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cmd defines all CLI commands and their flags
package cmd

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/apex/log"
	"github.com/apex/log/handlers/json"
	"github.com/apex/log/handlers/multi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/cisco-open/fsoc/cmd/config"
	"github.com/cisco-open/fsoc/cmd/version"
	"github.com/cisco-open/fsoc/logfilter"
)

var cfgFile string
var cfgProfile string
var outputFormat string

// rootCmd represents the base command when called without any subcommands
// TODO: replace github link "for more info" with Cisco DevNet link for fsoc once published
var rootCmd = &cobra.Command{
	Use:   "fsoc",
	Short: "fsoc - Cisco FSO Platform Control Tool",
	Long: `fsoc is an internal Cisco utility that serves as an entry point for developers on the 
Full Stack Observability (FSO) Platform.
It allows developers to interact with the product environments--developer, test and production--in a
uniform way and to perform common tasks. fsoc targets developers building the platform itself, as well
as developers building solutions on the platform.

Examples:
$ fsoc login
$ fsoc uql query "FETCH id, type, attributes FROM entities(k8s:workload)"
$ fsoc solution list
$ fsoc solution list -o json

For more information, see https://github.com/cisco-open/fsoc 

NOTE: fsoc is in alpha; breaking changes may occur`,
	PersistentPreRun:  preExecHook,
	TraverseChildren:  true,
	DisableAutoGenTag: true,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", fmt.Sprintf("config file (default is %s)", config.DefaultConfigFile))
	rootCmd.PersistentFlags().StringVar(&cfgProfile, "profile", "", "access profile (default is current or \"default\")")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "auto", "output format (auto, table, detail, json, yaml)")
	rootCmd.PersistentFlags().String("fields", "", "perform specified fields transform/extract JQ expression")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable detailed output")
	rootCmd.PersistentFlags().String("log", path.Join(os.TempDir(), "fsoc.log"), "determines the location of the fsoc log file")
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetIn(os.Stdin)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".fsoc" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".fsoc")
	}

	viper.AutomaticEnv() // read in environment variables that match
}

func registerSubsystem(cmd *cobra.Command) {
	rootCmd.AddCommand(cmd)
}

func helperFlagFormatter(fs *pflag.FlagSet) string {
	s := ""
	if fs != nil {
		fs.Visit(func(f *pflag.Flag) {
			if s != "" {
				s += " "
			}
			s += fmt.Sprintf("%v=%q", f.Name, f.Value)
		})
	}
	return "[" + s + "]"
}

// preExecHook is executed after the command line is parsed but
// before the command's handler is executed
func preExecHook(cmd *cobra.Command, args []string) {
	logLocation, _ := cmd.Flags().GetString("log")
	var file *os.File
	var cliHandler log.Handler

	if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
		cliHandler = logfilter.New(os.Stderr, log.InfoLevel)
	} else {
		cliHandler = logfilter.New(os.Stderr, log.WarnLevel)
	}
	log.SetLevel(log.InfoLevel)

	_ = os.Truncate(logLocation, 0)
	file, err := os.Create(logLocation)
	if err != nil {
		log.Warnf("failed to create log at %s", logLocation)
		log.SetHandler(cliHandler)
	} else {
		jsonHandler := json.New(file)
		log.SetHandler(multi.New(cliHandler, jsonHandler))
	}

	log.WithFields(version.GetVersion()).Info("fsoc version")

	log.WithFields(log.Fields{
		"command":   cmd.Name(),
		"arguments": fmt.Sprintf("%q", args),
		"flags":     helperFlagFormatter(cmd.Flags())}).
		Info("fsoc command line")

	// override the config file's current profile if --profile option is present
	if cmd.Flags().Changed("profile") {
		profile, _ := cmd.Flags().GetString("profile")
		if profile != "" { // allow empty string on cmd line to mean use current
			config.SetSelectedProfile(profile)
		}
	}

	// Determine if a configured profile is required for this command
	// (bypassed only for commands that must work or can safely work without it)
	bypass := bypassConfig(cmd) || cmd.Name() == "help" || isCompletionCommand(cmd)

	// try to read the config file.and profile
	err = viper.ReadInConfig()
	if err == nil {
		profile := config.GetCurrentProfileName()
		exists := config.GetCurrentContext() != nil
		if !exists && !bypass {
			log.Fatalf("fsoc is not fully configured: missing profile %q; please use \"fsoc config set\" to configure it", profile)
		}
		log.WithFields(log.Fields{
			"config_file": viper.ConfigFileUsed(),
			"profile":     profile,
			"existing":    exists,
		}).
			Info("fsoc context")
	} else {
		if bypass {
			log.Infof("Unable to read config file (%v), proceeding without a config", err)
		} else {
			log.Fatalf("fsoc is not configured, please use \"fsoc config set\" to configure an initial context")
		}
	}
}

func bypassConfig(cmd *cobra.Command) bool {
	_, bypassConfig := cmd.Annotations[config.AnnotationForConfigBypass]
	return bypassConfig
}

func isCompletionCommand(cmd *cobra.Command) bool {
	p := cmd.Parent()
	return (p != nil && p.Name() == "completion")
}
