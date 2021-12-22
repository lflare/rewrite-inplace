GOFLAGS := go build -ldflags "-s -w" .

.PHONY : all
all : build_linux

.PHONY : build_linux
build_linux :
	CGO_ENABLED=0 GOOS=linux $(GOFLAGS)

.PHONY : clean
clean :
	rm zfs-inplace
