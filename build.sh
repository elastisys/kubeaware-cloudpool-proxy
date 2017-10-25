#!/bin/bash

set -e

scriptname=$(basename ${0})
scriptdir=$(dirname ${0})

run_tests=true
build_docker_image=false

for arg in $@; do
    case ${arg} in
	--notest)
	    run_tests=false
	    ;;
    --docker)
        build_docker_image=true
        ;;
	--help)
	    echo "usage: ${scriptname} [OPTIONS] [command ...]"
	    echo ""
	    echo "Builds one or more commands under cmd/ and optionally runs "
	    echo "tests. If no commands are specified, all cmd/* are built."
	    echo ""
	    echo "Options:"
	    echo "--notest Skip tests."
        echo "--docker Build a docker image."
	    echo "--help   Print help message."
	    exit 0
	    ;;
	*)
	    # assume only positional arguments left
	    break
	    ;;
    esac
    shift
done


function build() {
    # single parameter: the command to build
    cmd=${1}

    pushd ${scriptdir} > /dev/null 
    
    destdir=./bin
    mkdir -p ${destdir}
    echo "[${scriptname}] building ${cmd} under ${destdir}/${cmd} ..."
    go build -o ${destdir}/${cmd} ./cmd/${cmd}

    popd > /dev/null
}


if [ "${1}" != "" ]; then
    # build a particular (set of) command(s) given on command-line
    for cmd in ${@}; do
	build ${cmd}
    done
else
    # build all commands
    for cmd in $(ls ${scriptdir}/cmd); do
	build ${cmd}
    done
fi

if ${run_tests}; then
    # run tests with coverage
    coverage_dir=./build/coverage
    echo "[${scriptname}] running tests (writing coverage to ${coverage_dir}) ..."
    mkdir -p ${coverage_dir}
    for pkg in $(ls pkg/); do
	go test -cover -coverprofile=${coverage_dir}/${pkg}.out ./pkg/${pkg}
    done

    echo "[${scriptname}] to view coverage: go tool cover -html ${coverage_dir}/<pkg>.out"
fi

if ${build_docker_image}; then
    pushd ${scriptdir} > /dev/null 

    # build for alpine (which uses a different libc)
    echo "[${scriptname}] building alpine-specific binary ..."
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
      -o build/kubeaware-cloudpool-proxy-alpine \
      ./cmd/kubeaware-cloudpool-proxy

    version=$(cat VERSION.txt)
    tag="elastisys/kubeaware-cloudpool-proxy:${version}"
    echo "[${scriptname}] building docker image ${tag} ..."
    docker build --tag=${tag} .

    popd > /dev/null
fi