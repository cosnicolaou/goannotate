package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"runtime"
	"strings"
)

type AnnotationConfig struct {
	ContextType string `json:"context_type"`
}

type Debug struct {
	CPUProfile string `json:"cpu_profile"`
}

type Options struct {
	Concurrency int `json:"concurrency"`
}

type Config struct {
	Interfaces []string         `json:"interfaces"`
	Functions  []string         `json:"functions"`
	Packages   []string         `json:"packages"`
	Annotation AnnotationConfig `json:"annotation"`
	Debug      Debug            `json:"debug"`
	Options    Options          `json:"options"`
}

func ConfigFromFile(filename string) (*Config, error) {
	config := &Config{}
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}
	if err := json.Unmarshal(buf, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %v: %v", filename, err)
	}
	config.configureDefaults()
	return config, err
}

func (c *Config) configureDefaults() {
	if c.Options.Concurrency == 0 {
		c.Options.Concurrency = runtime.NumCPU()
	}
}

func (c *Config) String() string {
	out := &strings.Builder{}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.Encode(c)
	return out.String()
}
