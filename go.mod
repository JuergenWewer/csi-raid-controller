module github.com/JuergenWewer/csi-raid-controller

go 1.15

require (
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/rclone/rclone v1.57.0
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	k8s.io/api v0.19.1
	k8s.io/apimachinery v0.19.1
	k8s.io/client-go v0.19.1
	k8s.io/klog/v2 v2.3.0
	sigs.k8s.io/sig-storage-lib-external-provisioner/v7 v7.0.1
)
