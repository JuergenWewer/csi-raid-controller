# cd <projectHomePath eg. csi-raid-controller>
# pwd -> csi-raid-controller
go mod init github.com/JuergenWewer/csi-raid-controller/controller
#will generate:
csi-raid-controller/go.mod

# to show go variables:
go env

# to deploy a new version to github repository
git add .
git commit -m "first klog create volume"
git push

generate the binary: controller

cd controller
make

if it's not installed:
go get -u github.com/tcnksm/ghr

git tag -a v0.0.9 -m "go mod init"
git push --tags


export GITHUB_TOKEN= <see in diary: git token jw>
export TAG=v0.0.9

push the release to the repository:
~/go/bin/ghr -t $GITHUB_TOKEN -r csi-raid-controller --replace --draft  $TAG controller


https://api.github.com/repos/JuergenWewer/csi-raid-controller/releases