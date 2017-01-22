VERSION = 0.2.5

clean:
	rm -rf build

build: clean
	GOOS=linux GOARCH=amd64 go build -o build/armor-$(VERSION)_linux-64 github.com/labstack/armor/cmd/armor
	GOOS=linux GOARCH=arm go build -o build/armor-$(VERSION)_linux-arm32 github.com/labstack/armor/cmd/armor
	GOOS=linux GOARCH=arm64 go build -o build/armor-$(VERSION)_linux-arm64 github.com/labstack/armor/cmd/armor
	GOOS=darwin GOARCH=amd64 go build -o build/armor-$(VERSION)_darwin-64 github.com/labstack/armor/cmd/armor
	GOOS=windows GOARCH=amd64 go build -o build/armor-$(VERSION)_windows-64.exe github.com/labstack/armor/cmd/armor
	docker build -t labstack/armor:$(VERSION) -t labstack/armor .

install:
	go install github.com/labstack/armor/cmd/armor

push: build
	docker push labstack/armor

.PHONY: clean build install push
