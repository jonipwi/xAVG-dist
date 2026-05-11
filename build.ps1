if (-not (Test-Path .\go.mod)) {
	go mod init xavg-client
}
go mod tidy
go build -o xavg-client.exe xavg-client.go

.\xavg-client.exe