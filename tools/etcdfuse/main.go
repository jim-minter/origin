package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/pkg/transport"
	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/kubernetes/pkg/api"

	_ "github.com/openshift/origin/pkg/api/install"
)

var endpoints = pflag.StringSlice("endpoints", []string{"https://127.0.0.1:4001"}, "")
var key = pflag.String("key", "/openshift.local.config/master/master.etcd-client.key", "")
var cert = pflag.String("cert", "/openshift.local.config/master/master.etcd-client.crt", "")
var cacert = pflag.String("cacert", "/openshift.local.config/master/ca.crt", "")

type fs struct {
	pathfs.FileSystem
	etcd *clientv3.Client
}

func (f *fs) GetAttr(name string, _ *fuse.Context) (*fuse.Attr, fuse.Status) {
	name = "/" + name

	resp, err := f.etcd.Get(context.Background(), name, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, fuse.EIO
	}

	if len(resp.Kvs) > 0 {
		k := string(resp.Kvs[0].Key)
		if k == name {
			return &fuse.Attr{Mode: fuse.S_IFREG | 0444}, fuse.OK
		}
		rel, err := filepath.Rel(name, k)
		if err == nil && !strings.HasPrefix(rel, "../") {
			return &fuse.Attr{Mode: fuse.S_IFDIR | 0555}, fuse.OK
		}
	}
	return nil, fuse.ENOENT
}

func (f *fs) OpenDir(name string, _ *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	name = "/" + name

	resp, err := f.etcd.Get(context.Background(), name, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, fuse.EIO
	}

	var dirents []fuse.DirEntry
	var lastName string

	for _, kv := range resp.Kvs {
		k := string(kv.Key)

		if k == name {
			return nil, fuse.ENOTDIR
		}

		rel, err := filepath.Rel(name, k)
		if err != nil {
			return nil, fuse.EIO
		}

		var mode uint32 = fuse.S_IFREG
		i := strings.IndexByte(rel, '/')
		if i != -1 {
			rel = rel[:i]
			mode = fuse.S_IFDIR
		}

		if rel != lastName {
			dirents = append(dirents, fuse.DirEntry{Name: rel, Mode: mode})
			lastName = rel
		}
	}

	if len(dirents) == 0 {
		return nil, fuse.ENOENT
	}

	return dirents, fuse.OK
}

func (f *fs) Open(name string, flags uint32, _ *fuse.Context) (nodefs.File, fuse.Status) {
	name = "/" + name

	resp, err := f.etcd.Get(context.Background(), name)
	if err != nil {
		return nil, fuse.EIO
	}

	if len(resp.Kvs) == 0 {
		return nil, fuse.ENOENT
	}

	if flags&fuse.O_ANYWRITE != 0 {
		return nil, fuse.EPERM
	}

	decoder := api.Codecs.UniversalDeserializer()
	encoder := json.NewSerializer(json.DefaultMetaFactory, api.Scheme, api.Scheme, true)

	obj, gvk, err := decoder.Decode(resp.Kvs[0].Value, nil, nil)
	if err != nil {
		return nil, fuse.EIO
	}
	obj.GetObjectKind().SetGroupVersionKind(*gvk)

	b := &bytes.Buffer{}
	err = encoder.Encode(obj, b)
	if err != nil {
		return nil, fuse.EIO
	}
	b.WriteByte('\n')

	return &nodefs.WithFlags{
		File:      nodefs.NewDataFile(b.Bytes()),
		FuseFlags: fuse.FOPEN_DIRECT_IO,
	}, fuse.OK
}

func newEtcdClient() (*clientv3.Client, error) {
	tlsConfig, err := transport.TLSInfo{
		CertFile: *cert,
		KeyFile:  *key,
		CAFile:   *cacert,
	}.ClientConfig()
	if err != nil {
		return nil, err
	}

	return clientv3.New(clientv3.Config{
		Endpoints: *endpoints,
		TLS:       tlsConfig,
	})
}

func main() {
	pflag.Parse()

	if len(pflag.Args()) < 1 {
		fmt.Printf("usage: %s MOUNTPOINT", os.Args[0])
		os.Exit(1)
	}

	etcd, err := newEtcdClient()
	if err != nil {
		panic(err)
	}

	nfs := pathfs.NewPathNodeFs(&fs{
		FileSystem: pathfs.NewDefaultFileSystem(),
		etcd:       etcd},
		nil,
	)
	server, _, err := nodefs.MountRoot(pflag.Arg(0), nfs.Root(), nil)
	if err != nil {
		panic(err)
	}

	server.Serve()
}
