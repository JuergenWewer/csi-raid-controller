module github.com/JuergenWewer/csi-raid-controller/controller

go 1.15

require (
	github.com/prometheus/client_golang v1.11.0
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	k8s.io/api v0.19.1
	k8s.io/apimachinery v0.19.1
	k8s.io/client-go v0.19.1
	k8s.io/klog/v2 v2.3.0
	sigs.k8s.io/sig-storage-lib-external-provisioner/v7 v7.0.1
)
