// Package outbound holds helpers shared by the SMS, WeChat and Alipay clients.
package outbound

import (
	"errors"
	"fmt"
	"net/url"
)

// Redact drops the request URL, whose query carries secrets, codes and phone numbers, from a
// transport error.
func Redact(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	u, parseErr := url.Parse(urlErr.URL)
	if parseErr != nil {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}

	return fmt.Errorf("%s %s://%s%s: %w", urlErr.Op, u.Scheme, u.Host, u.Path, urlErr.Err)
}
