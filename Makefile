VERSION := v0.1.0
GO := GO15VENDOREXPERIMENT="1" CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go

default: ucloud-build

update:
	glide up --strip-vendor --strip-vcs

ucloud-build:
	$(GO) build -o bin/ticloud-manager ucloud/main.go

ucloud-docker: ucloud-build
	mkdir -p docker/bin && cp bin/ticloud-manager docker/bin/ticloud-manager
	cd docker && docker build --tag "pingcap/ticloud-manager:ucloud-$(VERSION)" .

ucloud-publish:
	docker tag "pingcap/ticloud-manager:ucloud-$(VERSION)" "uhub.ucloud.cn/pingcap/ticloud-manager:latest"
	docker push "uhub.ucloud.cn/pingcap/ticloud-manager:latest"

clean:
	$(GO) clean -i ./...
	rm -rf bin/ticloud-manager docker/bin/ticloud-manager

.PHONY: docker clean update
