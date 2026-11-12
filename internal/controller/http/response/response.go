// Package response writes the {code, message, data} envelope shared with nestjs-template.
package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	contentType    = "application/json; charset=utf-8"
	successCode    = http.StatusOK
	successMessage = "success"
)

type success struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type successVoid struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Failure is the error envelope; Data is null when absent.
type Failure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// WriteData writes {code: 200, message: "success", data} with the given HTTP status.
func WriteData(w http.ResponseWriter, status int, data any) error {
	return WriteJSON(w, status, success{Code: successCode, Message: successMessage, Data: data})
}

// WriteOK writes {code: 200, message: "success"} without a data key.
func WriteOK(w http.ResponseWriter, status int) error {
	return WriteJSON(w, status, successVoid{Code: successCode, Message: successMessage})
}

// WriteError writes {code: status, message, data}.
func WriteError(w http.ResponseWriter, status int, message string, data any) error {
	return WriteJSON(w, status, Failure{Code: status, Message: message, Data: data})
}

// Encode serializes v like JSON.stringify: no HTML escaping, no trailing newline.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("response: encode: %w", err)
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// WriteJSON writes v as an application/json body.
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	body, err := Encode(v)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("response: write: %w", err)
	}

	return nil
}
