// Command openapigen runs oapi-codegen with the operation ids listed in openapi/scope.yaml.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/util"
	"go.yaml.in/yaml/v3"
)

type configuration struct {
	codegen.Configuration `yaml:",inline"`

	Output string `yaml:"output"`
}

type scope struct {
	Operations []string `yaml:"operations"`
}

const (
	_outputDirMode  = 0o750
	_outputFileMode = 0o600
)

var errEmptyScope = errors.New("scope has no operations")

func main() {
	configPath := flag.String("config", "openapi/oapi-codegen.yaml", "oapi-codegen configuration")
	scopePath := flag.String("scope", "openapi/scope.yaml", "operation ids to generate")
	specPath := flag.String("spec", "openapi/nestjs-baseline.json", "OpenAPI document")

	flag.Parse()

	if err := run(*configPath, *scopePath, *specPath); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen:", err)
		os.Exit(1)
	}
}

func run(configPath, scopePath, specPath string) error {
	var cfg configuration
	if err := readYAML(configPath, &cfg); err != nil {
		return err
	}

	var sc scope
	if err := readYAML(scopePath, &sc); err != nil {
		return err
	}

	if len(sc.Operations) == 0 {
		return errEmptyScope
	}

	cfg.OutputOptions.IncludeOperationIDs = sc.Operations

	strict := true
	if cfg.OutputOptions.Overlay.Strict != nil {
		strict = *cfg.OutputOptions.Overlay.Strict
	}

	spec, err := util.LoadSwaggerWithOverlay(specPath, util.LoadSwaggerWithOverlayOpts{
		Path:   cfg.OutputOptions.Overlay.Path,
		Strict: strict,
	})
	if err != nil {
		return fmt.Errorf("load %s: %w", specPath, err)
	}

	keepSuccessResponses(spec)

	code, err := codegen.Generate(spec, cfg.Configuration)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Output), _outputDirMode); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if err := os.WriteFile(cfg.Output, []byte(code), _outputFileMode); err != nil {
		return fmt.Errorf("write %s: %w", cfg.Output, err)
	}

	return nil
}

// keepSuccessResponses drops non-2xx responses: handlers report failures by returning an error.
func keepSuccessResponses(spec *openapi3.T) {
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if op.Responses == nil {
				continue
			}

			for code := range op.Responses.Map() {
				if !strings.HasPrefix(code, "2") {
					op.Responses.Delete(code)
				}
			}
		}
	}
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	return nil
}
