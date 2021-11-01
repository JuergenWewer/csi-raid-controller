go mod init github.com/JuergenWewer/csi-raid-controller/controller

to show go variables:
go env

git add .
git commit -m "jhjhjhj"
git push

generate the binary: controller

cd controller
make
#cd ..

if it's not installed:
go get -u github.com/tcnksm/ghr

# das ist wohl überflüssig - wohl doch nicht
git tag -a v0.0.8 -m "cd dir controller"
git push --tags

juergen.wewer@gmail.com

export GITHUB_TOKEN= <see in diary: git token jw>
export TAG=v0.0.8

push the release to the repository:
~/go/bin/ghr -t $GITHUB_TOKEN -r csi-raid-controller --replace --draft  $TAG controller


https://api.github.com/repos/JuergenWewer/csi-raid-controller/releases