package csiraidcontroller

import (
	//"context"
	//"fmt"
	//"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/sync"

	//"github.com/rclone/rclone/fs/config"
	//"github.com/rclone/rclone/fs/fspath"
	//"log"
	//"path"

	"context"
	"fmt"
	"github.com/rclone/rclone/fs/fspath"
	"log"
	"path"

	_ "github.com/rclone/rclone/backend/drive"
	_ "github.com/rclone/rclone/backend/local"
	_ "github.com/rclone/rclone/backend/sftp"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configfile"
)

func csisync(ctx context.Context, sourceDir string, destDir string) {
	fmt.Println("csisync called")

	var fsrc fs.Fs
	var fdst fs.Fs

	configfile.Install()

	fsrc, _ = NewFsFile(sourceDir)
	fmt.Printf("NewFsFile - f: %s \n", fsrc)

	fdst = newFsDir(ctx, destDir)

	fmt.Printf("fsrc: %s \n", fsrc)
	fmt.Printf("fdst: %s \n", fdst)
	entries, err := fsrc.List(context.Background(), "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(entries)
	err1 := sync.Sync(context.Background(), fdst, fsrc, true)
	if err1 != nil {
		log.Fatal(err1)
	}
	fmt.Println("copy done")
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

func newFsDir(ctx context.Context,remote string) fs.Fs {
	//f, err := cache.Get(nil, remote)
	//dir, file = config.
	config.SetConfigPath("/Users/wewer/.config/rclone/rclone.conf")
	fmt.Printf("newFsDir - config.GetConfigPath(): %s \n", config.GetConfigPath())
	config.Data().GetSectionList()
	fmt.Printf("newFsDir - config.Data().GetSectionList(): %s \n", config.Data().GetSectionList())
	//fsInfo, configName, fsPath, config, err := fs.ConfigFs(remote)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsInfo: %s \n", fsInfo.Name)
	//fmt.Printf("newFsDir - fs.ConfigFs - configName: %s \n", configName)
	//fmt.Printf("newFsDir - fs.ConfigFs - fsPath: %s \n", fsPath)
	//res, _ := config.Get("user")
	//fmt.Printf("newFsDir - fs.ConfigFs - config: %s \n", res)

	fsource, err := fs.NewFs(context.Background(),remote)
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
