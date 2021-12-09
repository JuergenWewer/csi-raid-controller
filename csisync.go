package csiraidcontroller

import (
	//"context"
	//"fmt"
	//"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/operations"
	_ "github.com/rclone/rclone/fs/operations"
	"github.com/rclone/rclone/fs/sync"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"strings"

	//"github.com/rclone/rclone/fs/config"
	//"github.com/rclone/rclone/fs/fspath"
	//"log"
	//"path"

	"context"
	"fmt"
	"github.com/rclone/rclone/fs/fspath"
	"log"
	"path"
	"time"

	_ "github.com/rclone/rclone/backend/drive"
	_ "github.com/rclone/rclone/backend/local"
	_ "github.com/rclone/rclone/backend/sftp"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configfile"
)

func csisyncNew(ctx context.Context, source string, target string, directory string, namespace string, name string) {
	fmt.Printf("csisync called source: %s, target: %s, directory: %s \n", source, target, directory)

	if len(source) == 0 {
		return
	}
	if len(target) == 0 {
		return
	}

	var fsrc fs.Fs
	var fdst fs.Fs

	configfile.Install()
	config.SetConfigPath("/csiraid.config")

	//fsrc, _ = NewFsFile(sourceDir)
	//fmt.Printf("NewFsFile - f: %s \n", fsrc)

	fsrc = newFsDir(ctx, source, directory, namespace, name)
	fdst = newFsDir(ctx, target, directory, namespace, name)

	fmt.Printf("fsrc: %s \n", fsrc)
	fmt.Printf("fdst: %s \n", fdst)
	//entries, err := fsrc.List(context.Background(), "")
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Printf("source entries: %s", entries)
	//entries, err = fdst.List(context.Background(), "")
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Printf("target entries: %s", entries)
	csisync(ctx, fsrc, fdst)
}

func csisyncVolume(ctx context.Context, source string, target string, directory string) {
	fmt.Printf("csisync called source: %s, target: %s, directory: %s \n", source, target, directory)

	if len(source) == 0 {
		return
	}
	if len(target) == 0 {
		return
	}

	var fsrc fs.Fs
	var fdst fs.Fs

	configfile.Install()
	config.SetConfigPath("/csiraid.config")

	//fsrc, _ = NewFsFile(sourceDir)
	//fmt.Printf("NewFsFile - f: %s \n", fsrc)

	fsrc = newFsDirFromVolume(ctx, source, directory)
	fdst = newFsDirFromVolume(ctx, target, directory)

	fmt.Printf("fsrc: %s \n", fsrc)
	fmt.Printf("fdst: %s \n", fdst)
	//entries, err := fsrc.List(context.Background(), "")
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Printf("source entries: %s", entries)
	//entries, err = fdst.List(context.Background(), "")
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Printf("target entries: %s", entries)
	csisync(ctx, fsrc, fdst)
}

func csisync(ctx context.Context,	fsrc fs.Fs, fdst fs.Fs) {

	ticker := time.NewTicker(1 * time.Second)
	var tickerRunning bool
	tickerRunning = true
	for _ = range ticker.C {
		fmt.Printf("tock for: %s\n", fsrc)
		entriesSource, errs := fsrc.List(context.Background(), "")
		if errs != nil {
			klog.Info(errs)
		}
		entriesDest, errd := fdst.List(context.Background(), "")
		if errd != nil {
			klog.Info(errd)
		}
		fmt.Printf("Source entries: %s Destination entries: %s \n", entriesSource, entriesDest)
		//check if sync have to be stopped
		if entriesSource.Len() == 0 && entriesDest.Len() == 0 {
			fmt.Printf("SYNCHRONISATION will be stopped\n")
			tickerRunning = false
			ticker.Stop()
		}
		//check if recovery is neccesssary
		if entriesSource.Len() == 0 && entriesDest.Len() > 0 {
			fmt.Printf("RECOVERY is starting\n")
			tickerRunning = false
			err1 := sync.Sync(context.Background(), fsrc,fdst, false)
			tickerRunning = true
			if err1 != nil {
				klog.Info("Failed to RECOVERY: " + fdst.String())
			}
			fmt.Printf("RECOVERY done for volume: %s \n", fdst)
		}

		if tickerRunning {
			fmt.Printf("sync starting for volume: %s \n", fsrc)
			err1 := sync.Sync(context.Background(), fdst, fsrc, false)
			if err1 != nil {
				klog.Info("Failed to sync fsrc: " + fsrc.String())
			}
			fmt.Printf("sync done for volume: %s \n", fsrc)
		} else {
			fmt.Printf("tickerRunning is false for %s \n", fsrc)
		}
	}
}


func csidelete(ctx context.Context, source string, target string, volume *v1.PersistentVolume) {
	fmt.Printf("csidelete called source: %s, target: %s, path: %s \n", source, target, volume.Spec.NFS.Path)

	if len(source) == 0 {
		return
	}
	if len(target) == 0 {
		return
	}

	configfile.Install()
	config.SetConfigPath("/csiraid.config")
	var fsrc fs.Fs
	fsrc = newFsDirFromVolume(ctx, target, volume.Spec.NFS.Path)

	fmt.Printf("delete fsrc: %s \n", fsrc)
	err := operations.Purge(context.Background(), fsrc, "")
	if err != nil {
		klog.Info("Failed to delete fsrc: " + fsrc.String())
	}
}

func NewFsFile(remote string) (fs.Fs, string) {
	_, fsPath, err := fspath.SplitFs(remote)
	if err != nil {
		err = fs.CountError(err)
		log.Fatalf("Failed to create file system for %q: %v", remote, err)
	}
	f, err := cache.Get(context.Background(), remote)
	switch err {
	case fs.ErrorIsFile:
		cache.Pin(f) // pin indefinitely since it was on the CLI
		return f, path.Base(fsPath)
	case nil:
		cache.Pin(f) // pin indefinitely since it was on the CLI
		return f, ""
	default:
		err = fs.CountError(err)
		log.Fatalf("Failed to create file system for %q: %v", remote, err)
	}
	return nil, ""
}

func newFsDirFromVolume(ctx context.Context, remote string, directory string) fs.Fs {
	fmt.Printf("newFsDir - config.GetConfigPath(): %s \n", config.GetConfigPath())
	fmt.Printf("newFsDir - config.Data().GetSectionList(): %s \n", config.Data().GetSectionList())
	path, _ := config.Data().GetValue(remote,"path")
	fmt.Printf("newFsDir - config.Data().GetValue(remote,\"path\"): %s \n", path)
	config.Data().GetValue(remote,"path")
	parts := strings.Split(directory, "/")
	var relDirectory string
	if len(parts) >= 1 {
		fmt.Printf("length: %d\n", len(parts))
		fmt.Printf("last element: %s\n", parts[len(parts)-1])
		relDirectory = parts[len(parts)-1]
	} else {
		relDirectory = directory
	}

	//fsInfo, configName, fsPath, config, err := fs.ConfigFs(remote)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsInfo: %s \n", fsInfo.Name)
	//fmt.Printf("newFsDir - fs.ConfigFs - configName: %s \n", configName)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsPath: %s \n", fsPath)
	//res, _ := config.Get("user")
	//fmt.Printf("newFsDir - fs.ConfigFs - config: %s \n", res)

	fsource, err := fs.NewFs(context.Background(),remote +":"+path + "/" + relDirectory)
	if err != nil {
		err = fs.CountError(err)
		fmt.Printf("fs.NewFs Failed to create file system for %q: %v \n", remote, err)
	}
	fmt.Printf("newFsDir - fsource: %s \n", fsource)

	//f, err := cache.Get(context.Background(), "remotetest:")
	////f, err := cache.GetFn(nil, remote, fs.NewFs)
	//fmt.Printf("newFsDir - f: %s \n", f)
	//if err != nil {
	//	err = fs.CountError(err)
	//	log.Fatalf("cache.Get Failed to create file system for %q: %v", remote, err)
	//}
	//cache.Pin(f) // pin indefinitely since it was on the CLI
	return fsource
}

func newFsDir(ctx context.Context, remote string, directory string, namespace string, name string) fs.Fs {
	//fmt.Printf("newFsDir - config.GetConfigPath(): %s \n", config.GetConfigPath())
	//fmt.Printf("newFsDir - config.Data().GetSectionList(): %s \n", config.Data().GetSectionList())
	path, _ := config.Data().GetValue(remote,"path")
	fmt.Printf("newFsDir - config.Data().GetValue(remote,\"path\"): %s \n", path)
	config.Data().GetValue(remote,"path")
	//fsInfo, configName, fsPath, config, err := fs.ConfigFs(remote)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsInfo: %s \n", fsInfo.Name)
	//fmt.Printf("newFsDir - fs.ConfigFs - configName: %s \n", configName)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsPath: %s \n", fsPath)
	//res, _ := config.Get("user")
	//fmt.Printf("newFsDir - fs.ConfigFs - config: %s \n", res)

	fsource, err := fs.NewFs(context.Background(),remote +":"+path+"/"+ namespace + "-" + name + "-" +directory)
	if err != nil {
		err = fs.CountError(err)
		fmt.Printf("fs.NewFs Failed to create file system for %q: %v \n", remote, err)
	}
	fmt.Printf("newFsDir - fsource: %s \n", fsource)

	//f, err := cache.Get(context.Background(), "remotetest:")
	////f, err := cache.GetFn(nil, remote, fs.NewFs)
	//fmt.Printf("newFsDir - f: %s \n", f)
	//if err != nil {
	//	err = fs.CountError(err)
	//	log.Fatalf("cache.Get Failed to create file system for %q: %v", remote, err)
	//}
	//cache.Pin(f) // pin indefinitely since it was on the CLI
	return fsource
}
