// Copyright 2019-2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

var scriptHashes = []string{
	// From static/frontend/fetch/fetch.tmpl
	"'sha256-DVdvl49HC0iGx/YKQq/kVNATnEdzGfExbJVTHqT95l8='",
	// From static/frontend/frontend.tmpl
	"'sha256-CoGrkqEM1Kjjf5b1bpcnDLl8ZZLAsVX+BoAzZ5+AOmc='",
	"'sha256-Rex7jo7NdAFHm6IM8u1LgCIn9Gr9p2QZ0bf6ZkK618g='",
	"'sha256-karKh1IrXOF1g+uoSxK+k9BuciCwYY/ytGuQVUiRzcM='",
	// From static/frontend/styleguide/styleguide.tmpl
	"'sha256-bL+cN9GtUg5dqjPwDiPJq4yfiEvOyEJ3rfw/YkNIAWc='",
	// From static/frontend/unit/main/main.tmpl
	"'sha256-UiVwSVJIK9udADqG5GZe+nRUXWK9wEot2vrxL4D2pQs='",
	// From static/frontend/unit/unit.tmpl
	"'sha256-cB+y/oSfWGFf7lHk8KX+ZX2CZQz/dPamIICuPvHcB6w='",
	// From static/frontend/unit/versions/versions.tmpl
	"'sha256-7mi5SPcD1cogj2+ju8J/+/qJG99F6Qo+3pO4xQkRf6Q='",
	// From static/legacy/html/pages/unit.tmpl
	"'sha256-V0I0c9gVBohHALcsk23X2c1nd3GO+Kpc1BNCpLhEj7Y='",
	// From static/legacy/html/pages/unit_details.tmpl
	"'sha256-bHZGfbft0NNI4pr8JS2ajCVFIrvcY1o07hbUL2Lfdls='",
	"'sha256-NgMe1ssApnbzZAEDkxSBAFfCNRfW6F7ajTmp08jUrPI='",
	"'sha256-lK9quwyQtvjVXRYCc2nYBfam6X9NN7FitPdCEVd3wpE='",
	// From static/legacy/html/pages/unit_versions.tmpl
	"'sha256-86HQcJ6uexGUBJWyPdp/1pozG9N7B3EUGT0ooKXwWzY='",
	// From static/worker/index.tmpl
	"'sha256-rEbn/zvLCsDDvDrVWQuUkKGEQsjQjFvIvJK4NVIMqZ4='",
}

// SecureHeaders adds a content-security-policy and other security-related
// headers to all responses.
func SecureHeaders(enableCSP bool) Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csp := []string{
				// Disallow plugin content: pkg.go.dev does not use it.
				"object-src 'none'",
				// Disallow <base> URIs, which prevents attackers from changing the
				// locations of scripts loaded from relative URLs. The site doesn’t have
				// a <base> tag anyway.
				"base-uri 'none'",
				fmt.Sprintf("script-src 'unsafe-inline' 'strict-dynamic' https: http: %s",
					strings.Join(scriptHashes, " ")),
			}
			if enableCSP {
				w.Header().Set("Content-Security-Policy", strings.Join(csp, "; "))
			}
			// Don't allow frame embedding.
			w.Header().Set("X-Frame-Options", "deny")
			// Prevent MIME sniffing.
			w.Header().Set("X-Content-Type-Options", "nosniff")

			h.ServeHTTP(w, r)
		})
	}
}
