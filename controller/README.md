# cd <projectHomePath eg. csi-raid-controller>
# pwd -> csi-raid-controller
go mod init github.com/JuergenWewer/csi-raid-controller/controller
# will generate:
csi-raid-controller/go.mod

# to show go variables:
go env

# to deploy a new version to github repository
generate the binary: controller

cd controller
make

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

git add .
git commit -m "first klog create volume"
git push

if it's not installed:
go get -u github.com/tcnksm/ghr

git tag -a v0.0.10 -m "first klog create volume"
git push --tags

# Make sure the current code is all checked in
git commit -am 'Ready for release v0.0.11'
# Now tag it
git tag v0.0.11
# Push the tag
git push origin v0.0.11
# Push the code
git push

export GITHUB_TOKEN= <see in diary: git token jw>
export TAG=v0.0.10

push the release to the repository:
~/go/bin/ghr -t $GITHUB_TOKEN -r csi-raid-controller --replace --draft  $TAG controller


https://api.github.com/repos/JuergenWewer/csi-raid-controller/releases