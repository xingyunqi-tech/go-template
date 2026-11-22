// Command openapigen bundles openapi/src into openapi/openapi.json and the fully inlined
// validation document, then generates the oapi-codegen server code from the bundle.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"
	"go.yaml.in/yaml/v3"
)

type configuration struct {
	codegen.Configuration `yaml:",inline"`

	Output string `yaml:"output"`
}

const (
	_outputDirMode  = 0o750
	_outputFileMode = 0o600
)

func main() {
	src := flag.String("src", "openapi/src", "spec source directory")
	out := flag.String("out", "openapi/openapi.json", "bundled document")
	validation := flag.String("validation", "internal/controller/http/validation.json", "inlined validation document")
	configPath := flag.String("config", "openapi/oapi-codegen.yaml", "oapi-codegen configuration")

	flag.Parse()

	if err := run(*src, *out, *validation, *configPath); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen:", err)
		os.Exit(1)
	}
}

func run(src, out, validation, configPath string) error {
	doc, err := Bundle(src)
	if err != nil {
		return err
	}

	bundled, err := Marshal(doc)
	if err != nil {
		return err
	}

	inlined, err := Inline(doc)
	if err != nil {
		return err
	}

	inlinedJSON, err := Marshal(inlined)
	if err != nil {
		return err
	}

	for path, data := range map[string][]byte{out: bundled, validation: inlinedJSON} {
		if err := write(path, data); err != nil {
			return err
		}
	}

	return generate(bundled, configPath)
}

func generate(bundled []byte, configPath string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg configuration
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}

	spec, err := openapi3.NewLoader().LoadFromData(bundled)
	if err != nil {
		return fmt.Errorf("load bundle: %w", err)
	}

	keepSuccessResponses(spec)

	code, err := codegen.Generate(spec, cfg.Configuration)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	return write(cfg.Output, []byte(code))
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

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), _outputDirMode); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, data, _outputFileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
