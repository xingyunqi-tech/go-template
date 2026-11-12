// Package view holds the response shapes written by v1 handlers.
package view

// AppInfo is the GET / payload.
type AppInfo struct {
	Name string `json:"name"`
}
