VERSION ?= $(shell cat VERSION)
export GOWORK := off
HAPROXY_IMAGE := docker.io/library/haproxy:2.8@sha256:f9e68f79bc73bd2e50527f8263e6a58ce875a2db695423c268bf95ee7230d641
.PHONY: build check package image haproxy-check
build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid= -X main.version=$(VERSION)' -o bin/clo-edge ./cmd/clo-edge
check:
	go test -race ./...
	go vet ./...
	python3 -m json.tool deploy/config.example.json > /dev/null
	for evidence in docs/*.json; do python3 -m json.tool "$$evidence" > /dev/null; done
haproxy-check: build
	bin/clo-edge render-haproxy --config deploy/config.example.json --edge-id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa > bin/haproxy.cfg
	docker run --rm --network none -i --entrypoint haproxy $(HAPROXY_IMAGE) -c -f /dev/stdin < bin/haproxy.cfg
package: build
	mkdir -p dist
	tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf dist/clo-edge-linux-amd64.tar -C bin clo-edge -C .. LICENSE NOTICE THIRD_PARTY_NOTICES.txt deploy docs/controller-operations.md
	gzip -n -f dist/clo-edge-linux-amd64.tar
	cp LICENSE NOTICE THIRD_PARTY_NOTICES.txt dist/
	cd dist && sha256sum clo-edge-linux-amd64.tar.gz LICENSE NOTICE THIRD_PARTY_NOTICES.txt > SHA256SUMS
image:
	docker buildx build --platform=linux/amd64 --provenance=false --build-arg VERSION=$(VERSION) -t local/clo-edge:$(VERSION) --load .
