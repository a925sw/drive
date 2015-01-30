// Copyright 2013 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	GDDirSuffix   = ".gd"
	PathSeparator = fmt.Sprintf("%c", os.PathSeparator)
)

type Context struct {
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	AbsPath      string `json:"-"`
}

type Index struct {
	FileId      string `json:"file_id"`
	Etag        string `json:"etag"`
	Md5Checksum string `json:"md5_checksum"`
	MimeType    string `json:"mime_type"`
	ModTime     int64  `json:"mod_time"`
	Version     int64  `json:"version"`
	Remote      bool   `json:"remote"`
}

type IndexFile struct {
	Name  string  `json:"name"`
	Index []Index `json:"index"`
}

type MountPoint struct {
	CanClean  bool
	Name      string
	AbsPath   string
	MountPath string
}

type Mount struct {
	CreatedMountDir   string
	ShortestMountRoot string
	Points            []*MountPoint
}

func (mpt *MountPoint) mounted() bool {
	// TODO: Find proper scheme for resolving symlinks
	return mpt.CanClean
}

func (mpt *MountPoint) Unmount() error {
	if mpt.mounted() {
		return os.RemoveAll(mpt.MountPath)
	}
	return nil
}

func (c *Context) AbsPathOf(fileOrDirPath string) string {
	return path.Join(c.AbsPath, fileOrDirPath)
}

func (c *Context) Read() (err error) {
	var data []byte
	if data, err = ioutil.ReadFile(credentialsPath(c.AbsPath)); err != nil {
		return
	}
	err = json.Unmarshal(data, c)
	return
}

func (c *Context) ReadIndices(p string) (*IndexFile, error) {
	var data []byte
	var err error
	if data, err = ioutil.ReadFile(indicesAbsPath(c.AbsPath)); err != nil {
		return nil, err
	}

	index := IndexFile{}
	err = json.Unmarshal(data, &index)
	return &index, err
}

func (c *Context) WriteIndices(index *IndexFile, p string) (err error) {
	var data []byte
	if data, err = json.Marshal(index); err != nil {
		return
	}
	return ioutil.WriteFile(indicesAbsPath(p), data, 0600)
}

func (c *Context) Write() (err error) {
	var data []byte
	if data, err = json.Marshal(c); err != nil {
		return
	}
	return ioutil.WriteFile(credentialsPath(c.AbsPath), data, 0600)
}

// Discovers the gd directory, if no gd directory or credentials
// could be found for the path, returns ErrNoContext.
func Discover(currentAbsPath string) (context *Context, err error) {
	p := currentAbsPath
	found := false
	for {
		info, e := os.Stat(gdPath(p))
		if e == nil && info.IsDir() {
			found = true
			break
		}
		newPath := path.Join(p, "..")
		if p == newPath {
			break
		}
		p = newPath
	}

	if !found {
		return nil, errors.New("no gd context is found; use gd init")
	}
	context = &Context{AbsPath: p}
	err = context.Read()
	return
}

func Initialize(absPath string) (pathGD string, firstInit bool, c *Context, err error) {
	pathGD = gdPath(absPath)
	sInfo, sErr := os.Stat(pathGD)
	if sErr != nil {
		if os.IsNotExist(sErr) {
			firstInit = true
		} else if !os.IsExist(sErr) { // An err not related to path existance
			return
		}
	}
	if sInfo != nil && !sInfo.IsDir() {
		err = fmt.Errorf("%s is not a directory", pathGD)
		return
	}
	if err = os.MkdirAll(pathGD, 0755); err != nil {
		return
	}
	c = &Context{AbsPath: absPath}
	err = c.Write()
	return
}

func gdPath(absPath string) string {
	return path.Join(absPath, GDDirSuffix)
}

func credentialsPath(absPath string) string {
	return path.Join(gdPath(absPath), "credentials.json")
}

func indicesAbsPath(absPath string) string {
	return path.Join(gdPath(absPath), "indices")
}

func LeastNonExistantRoot(contextAbsPath string) string {
	last := ""
	p := contextAbsPath
	for p != "" {
		fInfo, _ := os.Stat(p)
		if fInfo != nil {
			break
		}
		last = p
		p, _ = filepath.Split(strings.TrimRight(p, PathSeparator))
	}
	return last
}

func MountPoints(contextPath, contextAbsPath string, paths []string, hidden bool) (
	mount *Mount, sources []string) {

	createdMountDir := false
	shortestMountRoot := ""

	_, fErr := os.Stat(contextAbsPath)
	if fErr != nil {
		if !os.IsNotExist(fErr) {
			return
		}

		if sRoot := LeastNonExistantRoot(contextAbsPath); sRoot != "" {
			shortestMountRoot = sRoot
			sources = append(sources, sRoot)
		}

		mkErr := os.MkdirAll(contextAbsPath, os.ModeDir|0755)
		if mkErr != nil {
			fmt.Printf("mountpoint: %v\n", mkErr)
			return
		}

		createdMountDir = true
	}

	var mtPoints []*MountPoint
	visitors := map[string]bool{}

	for _, path := range paths {
		_, visited := visitors[path]
		if visited {
			continue
		}
		visitors[path] = true

		localinfo, err := os.Stat(path)
		if err != nil || localinfo == nil {
			continue
		}

		base := filepath.Base(path)
		if !hidden && strings.HasPrefix(base, ".") {
			continue
		}

		canClean := true
		mountPath := filepath.Join(contextAbsPath, base)
		err = os.Symlink(path, mountPath)

		if err != nil {
			if !os.IsExist(err) {
				continue
			}
			// This is an old symlink probably due to a name clash.
			// TODO: Due to the name clash, find a good name for this symlink.
			canClean = false
		}

		var relPath = ""
		if contextPath == "" {
			relPath = strings.Join([]string{"", base}, "/")
		} else {
			relPath = strings.Join([]string{"", contextPath, base}, "/")
		}

		mtPoints = append(mtPoints, &MountPoint{
			AbsPath:   path,
			CanClean:  canClean,
			MountPath: mountPath,
			Name:      relPath,
		})
	}
	if len(mtPoints) >= 1 {
		mount = &Mount{
			Points: mtPoints,
		}
		if createdMountDir {
			mount.CreatedMountDir = contextAbsPath
			mount.ShortestMountRoot = shortestMountRoot
		}
	}
	return
}
