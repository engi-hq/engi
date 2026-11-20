// Marks web/ as its own (empty) Go module so ./... and golangci-lint skip
// node_modules, where some npm packages ship Go files. There is no Go code here.
module github.com/engi-hq/engi/web

go 1.27.2
