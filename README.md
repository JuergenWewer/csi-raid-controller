go mod init github.com/JuergenWewer/csi-raid-controller

to show go variables:
go env

generate the binary: controller

make

if it's not installed:
go get -u github.com/tcnksm/ghr

git tag -a v0.0.2 -m "Release description"
git push --tags


export GITHUB_TOKEN=2aebdcc42a24511cc8570b50b01b756b9f75f49e
export TAG=v0.0.1

push the release to the repository:
~/go/bin/ghr -t $GITHUB_TOKEN -r csi-raid-controller --replace --draft  $TAG controller


https://api.github.com/repos/JuergenWewer/storage-lib-external-provisioner/releases