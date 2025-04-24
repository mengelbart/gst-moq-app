module github.com/mengelbart/gst-moq-app

go 1.23.6

toolchain go1.24.1

require (
	github.com/go-gst/go-gst v1.1.0
	github.com/mengelbart/moqtransport v0.3.1-0.20250422164651-511352b22796
	github.com/quic-go/quic-go v0.49.0
)

require (
	github.com/go-gst/go-glib v1.1.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/google/pprof v0.0.0-20250128161936-077ca0a936bf // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mengelbart/qlog v0.1.0 // indirect
	github.com/onsi/ginkgo/v2 v2.22.2 // indirect
	go.uber.org/mock v0.5.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20250128182459-e0ece0dbea4c // indirect
	golang.org/x/mod v0.22.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/tools v0.29.0 // indirect
)

replace github.com/mengelbart/moqtransport v0.3.1-0.20250422164651-511352b22796 => ../moqtransport
