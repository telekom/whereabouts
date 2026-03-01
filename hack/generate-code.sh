#!/usr/bin/env bash

BASEDIR=$(pwd)
${BASEDIR}/bin/controller-gen object crd:crdVersions=v1 paths="./pkg/api/..." output:crd:artifacts:config=doc/crds
cp doc/crds/*.yaml deployment/whereabouts-chart/crds/
