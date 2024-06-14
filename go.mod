module github.com/mengelbart/gst-moq-app

go 1.22.0

require (
	github.com/mengelbart/gst-go v0.0.4
	github.com/mengelbart/moqtransport v0.2.1-0.20240608071933-746b89cb6422
	github.com/quic-go/quic-go v0.43.1
)

require (
	github.com/Eyevinn/mp4ff v0.45.0
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/google/pprof v0.0.0-20240430035430-e4905b036c4e // indirect
	github.com/onsi/ginkgo/v2 v2.17.2 // indirect
	go.uber.org/mock v0.4.0 // indirect
	golang.org/x/crypto v0.22.0 // indirect
	golang.org/x/exp v0.0.0-20240416160154-fe59bbe5cc7f // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/net v0.24.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/tools v0.20.0 // indirect
)

replace github.com/mengelbart/moqtransport v0.2.1-0.20240608071933-746b89cb6422 => ../moqtransport
