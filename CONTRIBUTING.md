# Contributing to Gopher Distributed Crawler

Thank you for contributing! Please adhere to the engineering guidelines below:

1. **Code Standards:**
   - Format all Go code with `gofmt`.
   - Maintain SSRF validation (`pkg/crawler/safe_dialer.go`) across any outbound network changes.
   - Keep `web/` and `public/` synchronized via `npm run build`.
2. **Testing Requirements:**
   - Run `go vet ./...` and `go test -race ./...`.
   - Run `npm run lint && npm test`.
   - Never add fake workers, simulated metrics, or silent fallbacks in production handlers.
3. **Security Disclosure:**
   - Do not commit `.env` files, tokens, or credentials. Report security concerns carefully.
