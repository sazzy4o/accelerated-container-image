/*
   Copyright The Accelerated Container Image Authors

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	obdconv "github.com/containerd/accelerated-container-image/pkg/convertor"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/leases"
	_ "github.com/go-sql-driver/mysql"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var (
	emptyDesc  ocispec.Descriptor
	emptyLayer layer

	convSnapshotNameFormat = "overlaybd-conv-%s"
	convLeaseNameFormat    = convSnapshotNameFormat
)

var convertCommand = cli.Command{
	Name:        "obdconv",
	Usage:       "convert image layer into overlaybd format type",
	ArgsUsage:   "<src-image> <dst-image>",
	Description: `Export images to an OCI tar[.gz] into zfile format`,
	Flags: append(commands.RegistryFlags,
		cli.StringFlag{
			Name:  "fstype",
			Usage: "filesystem type(required), used to mount block device, support specifying mount options and mkfs options, separate fs type and options by ';', separate mount options by ',', separate mkfs options by ' '",
			Value: "ext4",
		},
		cli.StringFlag{
			Name:  "dbstr",
			Usage: "data base config string used for layer deduplication",
			Value: "",
		},
		cli.StringFlag{
			Name:  "algorithm",
			Usage: "compress algorithm uses in zfile, [lz4|zstd]",
			Value: "",
		},
		cli.IntFlag{
			Name:  "bs",
			Usage: "The size of a compressed data block in KB. Must be a power of two between 4K~64K [4/8/16/32/64])",
			Value: 0,
		},
	),
	Action: func(context *cli.Context) error {
		var (
			srcImage    = context.Args().First()
			targetImage = context.Args().Get(1)
		)

		if srcImage == "" || targetImage == "" {
			return errors.New("please provide src image name(must in local) and dest image name")
		}

		cli, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		ctx, done, err := cli.WithLease(ctx,
			leases.WithID(fmt.Sprintf(convLeaseNameFormat, uniquePart())),
			leases.WithExpiration(1*time.Hour),
		)
		if err != nil {
			return errors.Wrap(err, "failed to create lease")
		}
		defer done(ctx)

		var (
			convertOpts = []converter.Opt{}
			obdOpts     = []obdconv.Option{}
		)

		fsType := context.String("fstype")
		fmt.Printf("filesystem type: %s\n", fsType)
		obdOpts = append(obdOpts, obdconv.WithFsType(fsType))
		dbstr := context.String("dbstr")
		if dbstr != "" {
			obdOpts = append(obdOpts, obdconv.WithDbstr(dbstr))
			fmt.Printf("database config string: %s\n", dbstr)
		}
		algorithm := context.String("algorithm")
		obdOpts = append(obdOpts, obdconv.WithAlgorithm(algorithm))
		blockSize := context.Int("bs")
		obdOpts = append(obdOpts, obdconv.WithBlockSize(blockSize))

		resolver, err := commands.GetResolver(ctx, context)
		if err != nil {
			return err
		}
		obdOpts = append(obdOpts, obdconv.WithResolver(resolver))
		obdOpts = append(obdOpts, obdconv.WithImageRef(srcImage))
		obdOpts = append(obdOpts, obdconv.WithClient(cli))
		convertOpts = append(convertOpts, converter.WithIndexConvertFunc(obdconv.IndexConvertFunc(obdOpts...)))

		newImg, err := converter.Convert(ctx, cli, targetImage, srcImage, convertOpts...)
		if err != nil {
			return err
		}
		fmt.Printf("new image digest: %s\n", newImg.Target.Digest.String())
		return nil
	},
}

type layer struct {
	desc   ocispec.Descriptor
	diffID digest.Digest
}

// NOTE: based on https://github.com/containerd/containerd/blob/v1.4.3/rootfs/apply.go#L181-L187
func uniquePart() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.Nanosecond(), strings.Replace(base64.URLEncoding.EncodeToString(b[:]), "_", "-", -1))
}
