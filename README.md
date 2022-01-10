# Initialise the project
go mod init github.com/JuergenWewer/csi-raid-controller
## will generate:
csi-raid-controller/go.mod
## if  the erros: module k8s.io/api@latest found (v0.22.3), but does not contain package k8s.io/api/batch/v2alpha1 appear check go.mod
## it should contain:
```
require (
    github.com/prometheus/client_golang v1.11.0
    golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
    k8s.io/api v0.19.1
    k8s.io/apimachinery v0.19.1
    k8s.io/client-go v0.19.1
    k8s.io/klog/v2 v2.3.0
    sigs.k8s.io/sig-storage-lib-external-provisioner/v7 v7.0.1
)
```

# to deploy a new version v0.0.47 to github repository
## checkout the master branch
git checkout master
git pull

## create a new branch named as the version
git checkout -b "v0.0.36"

## make the modifications
...
delete controller

## generate the binary: controller - das ist nicht notwendig
make

## push the branch to github
git add .
git commit -m "release v0.0.36"
git push --set-upstream origin v0.0.36

## merge the release branch back into the master
git checkout master
git pull
git merge v0.0.36
git push

# use the csiraidcontroller in other go projects with version v0.0.47
in the .go file:
```
package main
import (
    ...
    "github.com/JuergenWewer/csi-raid-controller"
    ...
)
```
in the go.mod file enter:

```
module github.com/kubernetes-sigs/csi-raid

go 1.14

require (
    github.com/JuergenWewer/csi-raid-controller v0.0.47
    ...
    )
```
    

# go Hints
## show go variables:
go env
## run go application - main package is required
go run csiraidcontroller.go
## test the application
go test


# setup intellij for go
## import go plugin
## preferences/language & Feature/ go/ go module set
Enable go module integration
GOENV=/Users/wewer/Library/Application Support/go/env;GOMODCACHE=/Users/wewer/go/pkg/mod;GOPATH=/Users/wewer/go;GOCACHE=/Users/wewer/Library/Caches/go-build;GO111MODULE=on

## in shell environment the following env variables shoul be set
export GOMODCACHE=/Users/wewer/go/pkg/mod
export GOENV="/Users/wewer/Library/Application Support/go/env"
export GOPATH=/Users/wewer/go
export GO111MODULE=on
export GOCACHE=/Users/wewer/Library/Caches/go-build