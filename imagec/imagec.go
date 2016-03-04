// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"golang.org/x/net/context"

	log "github.com/Sirupsen/logrus"

	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/progress"
	"github.com/docker/docker/pkg/streamformatter"
	"github.com/vmware/vic/apiservers/portlayer/models"
)

var (
	options = ImageCOptions{}

	// https://raw.githubusercontent.com/docker/docker/master/distribution/pull_v2.go
	po = streamformatter.NewJSONStreamFormatter().NewProgressOutput(os.Stdout, false)
)

// ImageCOptions wraps the cli arguments
type ImageCOptions struct {
	registry string
	image    string
	digest   string

	destination string

	host string

	logfile string

	username string
	password string

	token *Token

	timeout time.Duration

	stdout bool
	debug  bool
}

// ImageWithMeta wraps the models.Image with some additional metadata
type ImageWithMeta struct {
	*models.Image

	layer   FSLayer
	history History
}

const (
	// DefaultDockerURL holds the URL of Docker registry
	DefaultDockerURL = "https://registry-1.docker.io/v2/"
	// DefaultDockerImage holds the default image name
	DefaultDockerImage = "library/photon"
	// DefaultDockerDigest holds the default digest name
	DefaultDockerDigest = "latest"

	// DefaultDestination specifies the default directory to use
	DefaultDestination = "."

	// DefaultPortLayerHost specifies the default port layer server
	DefaultPortLayerHost = "localhost:8080"

	// DefaultLogfile specifies the default log file name
	DefaultLogfile = "imagec.log"

	// DefaultHTTPTimeout specifies the default HTTP timeout
	DefaultHTTPTimeout = 3600 * time.Second

	// DefaultTokenExpirationDuration specifies the default token expiration
	DefaultTokenExpirationDuration = 60 * time.Second
)

func init() {
	flag.StringVar(&options.registry, "registry", DefaultDockerURL, "Address of the registry")
	flag.StringVar(&options.image, "image", DefaultDockerImage, "Name of the image")
	flag.StringVar(&options.digest, "digest", DefaultDockerDigest, "Tag name or image digest")

	flag.StringVar(&options.destination, "destination", DefaultDestination, "Destination directory")

	flag.StringVar(&options.host, "host", DefaultPortLayerHost, "Host that runs portlayer API (FQDN:port format)")

	flag.StringVar(&options.logfile, "logfile", DefaultLogfile, "Path of the installer log file")

	flag.StringVar(&options.username, "username", "", "Username")
	flag.StringVar(&options.password, "password", "", "Password")

	flag.DurationVar(&options.timeout, "timeout", DefaultHTTPTimeout, "HTTP timeout")

	flag.BoolVar(&options.stdout, "stdout", false, "Enable writing to stdout")
	flag.BoolVar(&options.debug, "debug", true, "Enable debugging")

	flag.Parse()
}

func main() {
	// Open the log file
	f, err := os.OpenFile(options.logfile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open the logfile %s: %s", options.logfile, err)
	}
	defer f.Close()

	// Set the log level
	if options.debug {
		log.SetLevel(log.DebugLevel)
	}

	// Initiliaze logger with default TextFormatter
	log.SetFormatter(&log.TextFormatter{DisableColors: true, FullTimestamp: true})

	// SetOutput to log file and/or stdout
	log.SetOutput(f)
	if options.stdout {
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}

	// Hostname is our storename
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("Failed to return the host name: %s", err)
	}

	// Ping the server to ensure it's at least running
	ok, err := PingPortLayer()
	if err != nil || !ok {
		log.Fatalf("Failed to ping portlayer: %s", err)
	}

	// Get the URL of the OAuth endpoint
	url, err := LearnAuthURL(options)
	if err != nil {
		log.Fatalf("Failed to obtain OAuth endpoint: %s", err)
	}

	// Get the OAuth token
	token, err := FetchToken(url)
	if err != nil {
		log.Fatalf("Failed to fetch OAuth token: %s", err)
	}
	options.token = token

	// Get the manifest
	manifest, err := FetchImageManifest(options)
	if err != nil {
		log.Fatalf("Failed to fetch image manifest: %s", err)
	}

	progress.Message(po, options.digest, "Pulling from "+options.image)

	// List of ImageWithMeta to hold Image structs
	images := make([]ImageWithMeta, len(manifest.FSLayers))

	v1 := V1Compatibility{}
	// iterate from parent to children
	for i := len(manifest.History) - 1; i >= 0; i-- {
		history := manifest.History[i]
		layer := manifest.FSLayers[i]

		// unmarshall V1Compatibility to get the image ID
		if err := json.Unmarshal([]byte(history.V1Compatibility), &v1); err != nil {
			log.Fatalf("Failed to unmarshall image history: %s", err)
		}

		// if parent is empty set it to scratch
		parent := "scratch"
		if v1.Parent != "" {
			parent = v1.Parent
		}

		// add image to ImageWithMeta list
		images[i] = ImageWithMeta{
			Image: &models.Image{
				ID:     v1.ID,
				Parent: &parent,
				Store:  hostname,
			},
			history: history,
			layer:   layer,
		}
	}
	for i := range images {
		log.Debugf("Manifest image: %#v", images[i])
	}

	// Create the image store
	err = CreateImageStore(hostname)
	if err != nil {
		log.Fatalf("Failed to create image store: %s", err)
	}

	// FIXME: https://github.com/vmware/vic/issues/201
	// Get the list of existing images
	existingImages, err := ListImages(hostname)
	if err != nil {
		log.Fatalf("Failed to obtain list of images: %s", err)
	}
	for i := range existingImages {
		log.Debugf("Existing image: %#v", existingImages[i])
	}

	// iterate from parent to children
	// so that we can delete from the slice
	// while iterating over it
	for i := len(images) - 1; i >= 0; i-- {
		ID := images[i].ID
		if _, ok := existingImages[ID]; ok {
			log.Debugf("%s already exists", ID)
			// delete existing image from images
			images = append(images[:i], images[i+1:]...)

			progress.Update(po, ID[:12], "Already exists")
		}
	}

	var wg sync.WaitGroup

	wg.Add(len(images))

	// iterate from parent to children
	// so that portlayer can extract each layer
	// on top of previous one
	results := make(chan error, len(images))
	for i := len(images) - 1; i >= 0; i-- {
		go func(image ImageWithMeta) {
			defer wg.Done()

			err := FetchImageBlob(options, &image)
			if err != nil {
				results <- fmt.Errorf("%s/%s returned %s", options.image, image.layer.BlobSum, err)
			} else {
				results <- nil
			}
		}(images[i])
	}
	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			log.Fatalf("Failed to fetch image blob: %s", err)
		}
	}

	// iterate from parent to children
	// so that portlayer can extract each layer
	// on top of previous one
	destination := path.Join(options.destination, options.image, options.digest)
	for i := len(images) - 1; i >= 0; i-- {
		image := images[i]

		id := image.Image.ID
		f, err := os.Open(path.Join(destination, id, id+".tar"))
		if err != nil {
			log.Fatalf("Failed to open file: %s", err)
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			log.Fatalf("Failed to stat file: %s", err)
		}

		in := progress.NewProgressReader(
			ioutils.NewCancelReadCloser(
				context.Background(), f),
			po,
			fi.Size(),
			id[:12],
			"Extracting",
		)
		defer in.Close()

		// Write the image
		// FIXME: send metadata when portlayer supports it
		err = WriteImage(&image, in)
		if err != nil {
			log.Fatalf("Failed to write to image store: %s", err)
		}
		progress.Update(po, id[:12], "Pull complete")
	}

	// FIXME: Dump the digest
	//progress.Message(po, "", "Digest: 0xDEAD:BEEF")

	if len(images) > 0 {
		progress.Message(po, "", "Status: Downloaded newer image for "+options.image+":"+options.digest)
	} else {
		progress.Message(po, "", "Status: Image is up to date for "+options.image+":"+options.digest)

	}
}