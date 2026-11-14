package view

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

// Health is the @nestjs/terminus result: details is info followed by error.
type Health struct {
	Status  string     `json:"status"`
	Info    Indicators `json:"info"`
	Error   Indicators `json:"error"`
	Details Indicators `json:"details"`
}

// Indicators is an ordered {key: {...fields, status}} object.
type Indicators []health.Indicator

// NewHealth converts a health result.
func NewHealth(r *health.Result) Health {
	status := "ok"
	if !r.OK() {
		status = "error"
	}

	details := make(Indicators, 0, len(r.Info)+len(r.Error))
	details = append(append(details, r.Info...), r.Error...)

	return Health{Status: status, Info: r.Info, Error: r.Error, Details: details}
}

// MarshalJSON implements json.Marshaler.
func (in Indicators) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteByte('{')

	for i, ind := range in {
		if i > 0 {
			buf.WriteByte(',')
		}

		if err := writeIndicator(&buf, &ind); err != nil {
			return nil, err
		}
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

func writeIndicator(buf *bytes.Buffer, ind *health.Indicator) error {
	key, err := json.Marshal(ind.Key)
	if err != nil {
		return fmt.Errorf("view: health key: %w", err)
	}

	buf.Write(key)
	buf.WriteString(":{")

	for _, f := range ind.Fields {
		name, _ := json.Marshal(f.Name)

		value, err := json.Marshal(f.Value)
		if err != nil {
			return fmt.Errorf("view: health %s: %w", f.Name, err)
		}

		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
		buf.WriteByte(',')
	}

	if ind.Up {
		buf.WriteString(`"status":"up"}`)
	} else {
		buf.WriteString(`"status":"down"}`)
	}

	return nil
}
