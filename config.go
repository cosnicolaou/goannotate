package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"runtime"
	"strings"

	"gopkg.in/yaml.v2"
)

type AnnotationConfig struct {
	ContextType          string `yaml:"contextType"`
	Function             string `yaml:"function"`
	Import               string `yaml:"import"`
	Owner                string `yaml:"owner"`
	IgnoreEmptyFunctions bool   `yaml:"ignoreEmptyFunctions"`
}

type Debug struct {
	CPUProfile string `yaml:"cpu_profile"`
}

type Options struct {
	Concurrency int `yaml:"concurrency"`
}

type Config struct {
	Interfaces []string         `yam:"interfaces"`
	Functions  []string         `yam:"functions"`
	Packages   []string         `yam:"packages"`
	Annotation AnnotationConfig `yam:"annotation,flow"`
	Debug      Debug            `yam:"debug,flow"`
	Options    Options          `yam:"options,flow"`
}

func ConfigFromFile(filename string) (*Config, error) {
	config := &Config{}
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}
	err = yaml.Unmarshal(buf, config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	/*	if err := json.Unmarshal(buf, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %v: %v", filename, err)
	}*/
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
