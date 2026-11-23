VERSION ?= $(shell cat VERSION)
export GOWORK := off
HAPROXY_IMAGE := docker.io/library/haproxy:2.8@sha256:f9e68f79bc73bd2e50527f8263e6a58ce875a2db695423c268bf95ee7230d641
.PHONY: build check package image haproxy-check
build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid= -X main.version=$(VERSION)' -o bin/clo-edge ./cmd/clo-edge
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid= -X main.version=$(VERSION)' -o bin/clo-ingress ./cmd/clo-ingress
check:
	go test -race ./...
	go vet ./...
	python3 -m unittest discover -s deploy/ingress -p 'test_*.py'
	python3 -m json.tool deploy/config.example.json > /dev/null
	python3 -m json.tool deploy/ingress/config.example.json > /dev/null
	for evidence in docs/*.json; do python3 -m json.tool "$$evidence" > /dev/null; done
haproxy-check: build
	bin/clo-edge render-haproxy --config deploy/config.example.json --edge-id aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa > bin/haproxy.cfg
	docker run --rm --network none -i --entrypoint haproxy $(HAPROXY_IMAGE) -c -f /dev/stdin < bin/haproxy.cfg
	CLO_INGRESS_HAPROXY_TEST=1 go test ./internal/ingress -run TestNativeHAProxyGenerationRuntimeAndGracefulReload -count=1
package: build
	mkdir -p dist
	tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf dist/clo-edge-linux-amd64.tar -C bin clo-edge clo-ingress -C .. LICENSE NOTICE THIRD_PARTY_NOTICES.txt deploy docs/controller-operations.md docs/ingress-operations.md
	gzip -n -f dist/clo-edge-linux-amd64.tar
	cp LICENSE NOTICE THIRD_PARTY_NOTICES.txt dist/
	cd dist && sha256sum clo-edge-linux-amd64.tar.gz LICENSE NOTICE THIRD_PARTY_NOTICES.txt > SHA256SUMS
image:
	docker buildx build --platform=linux/amd64 --provenance=false --build-arg VERSION=$(VERSION) -t local/clo-edge:$(VERSION) --load .

.PHONY: monitoring-check
monitoring-check:
	docker run --rm --network none -v "$(CURDIR)/deploy/ingress:/etc/rave-clo-ingress:ro" -w /etc/rave-clo-ingress --entrypoint /bin/promtool quay.io/prometheus/prometheus@sha256:9332054523223e2ddc18d8101799d54a4a8d823b030c53216f063a6c117773cd test rules alerts.test.yml
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/promtool quay.io/prometheus/prometheus@sha256:9332054523223e2ddc18d8101799d54a4a8d823b030c53216f063a6c117773cd check config prometheus.example.yml
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/promtool quay.io/prometheus/prometheus@sha256:9332054523223e2ddc18d8101799d54a4a8d823b030c53216f063a6c117773cd test rules alerts.test.yml
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/amtool quay.io/prometheus/alertmanager@sha256:5f6e66c01bd610aef6dd671642a52eee775fd3a89bd8788ebada33efaf373d10 check-config alertmanager.example.yml
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/sh quay.io/prometheus/alertmanager@sha256:5f6e66c01bd610aef6dd671642a52eee775fd3a89bd8788ebada33efaf373d10 /etc/rave-clo-monitor/check-telegram-template.sh
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/amtool quay.io/prometheus/alertmanager@sha256:5f6e66c01bd610aef6dd671642a52eee775fd3a89bd8788ebada33efaf373d10 config routes test --config.file=alertmanager.example.yml --verify.receivers=discard environment=clo monitor=rave-clo acceptance_run=fixture
	docker run --rm --network none -v "$(CURDIR)/deploy/monitoring:/etc/rave-clo-monitor:ro" -w /etc/rave-clo-monitor --entrypoint /bin/blackbox_exporter quay.io/prometheus/blackbox-exporter@sha256:ea1c7aa3ef3f307cd03e85f34abb604478ccb90b567fc690da0cd9776da76b13 --config.file=blackbox.yml --config.check
