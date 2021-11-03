# Initialise the project
go mod init github.com/JuergenWewer/csi-raid-controller
# will generate:
csi-raid-controller/go.mod
# if  the erros: module k8s.io/api@latest found (v0.22.3), but does not contain package k8s.io/api/batch/v2alpha1 appear check go.mod
# it should contain:
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
git checkout -b "v0.0.47"

##make the modifications
...

#generate the binary: controller
make

#push the branch to github
git add .
git commit -m "release v0.0.47"
git push --set-upstream origin v0.0.47


# Hints: to show go variables:
go env
