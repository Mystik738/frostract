package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mystik738/frostexture"
	"github.com/Mystik738/frostlang"
	"github.com/aviddiviner/go-murmur"

	log "github.com/sirupsen/logrus"
)

const bytesPerFile = 29
const oldBytesPerFile = 17
const fphook = "FPHook.log"

func main() {
	overwrite := flag.Bool("o", false, "Overwrite existing files")
	help := flag.Bool("h", false, "Display this help")
	dds := flag.Bool("d", false, "Skip repairing dds files. Setting this flag will also skip conversion to png")
	png := flag.Bool("p", false, "Skip converting dds to png")
	pre121 := flag.Bool("v", false, "Idx is from before version 1.2.1")
	langToJSON := flag.Bool("j", false, "Skip converting lang files to json")

	flag.Parse()

	Frostract(*overwrite, *help, *dds, *png, *pre121, *langToJSON)
}

func Frostract(overwrite, help, dds, png, pre121, langToJSON bool) {
	log.SetLevel(log.DebugLevel)

	if help {
		helptext := `frostract is a utility for extracting files from Frostpunk idx and dat archives. Requires magick to be installed to convert dds to png. FPHook.log must be in same directory for filename lookup.
Usage: frostract [flags]

Flags:
 -h		Display this help
 -d		Skip repairing dds files. Setting this flag will also skip conversion to png
 -p		Skip converting dds to png
 -j		Skip converting lang files to json
 -o		Overwrite existing files
 -v		Idx is from before version 1.2.1`

		log.Info(helptext)
	} else {
		startTime := time.Now()
		dir, err := os.Getwd()
		//This is more a heuristic value to increase efficiency rather than an exact science
		concurrency := 8
		checkError(err)
		log.Debugf("Working directory is %v", dir)
		//Make our hashmap
		hashToFileName := make(map[string]string)
		lineCounts := make(map[string]int)
		if !os.IsNotExist(err) {
			//Get our FPHook file to get hashes
			f, err := os.Open(fphook)
			checkError(err)
			r := bufio.NewReader(f)
			lineCount := 0
			for {
				line, isPrefix, err := r.ReadLine()
				lineCount++
				if err != nil || isPrefix {
					break
				}
				splits := strings.Split(string(line), "/")
				fileName := splits[len(splits)-1]
				b := make([]byte, 4)
				binary.LittleEndian.PutUint32(b, murmur.MurmurHash2([]byte(line), 0))
				hashToFileName[hex.EncodeToString(b)] = fileName
				if _, ok := lineCounts[string(line)]; ok {
					lineCounts[string(line)]++
				} else {
					lineCounts[string(line)] = 1
				}
			}
			f.Close()
			//This is just to trim out duplicates, maybe flag to turn off
			if len(lineCounts) < lineCount {
				f, err = os.Create(fphook)
				checkError(err)

				keys := make([]string, 0, len(lineCounts))
				for k := range lineCounts {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				for _, line := range keys {
					f.Write([]byte(line))
					f.Write([]byte("\r\n"))
				}
				f.Close()
			}

			log.Debugf("FPHook.log has %v lines.", len(lineCounts))

			f.Close()
		} else {
			log.Warn("FPHook.log does not exist in current directory, cannot adjust filenames")
		}

		//Walk our current path to get idx files
		files := make([]string, 0)
		allFiles, err := ioutil.ReadDir(dir)
		checkError(err)
		for _, info := range allFiles {
			log.Debugf("Investigating %v", info.Name())
			if !info.IsDir() && filepath.Ext(info.Name()) == ".idx" {
				log.Debugf("Adding %v", info.Name())
				files = append(files, info.Name())
			}
		}
		checkError(err)
		for _, idxFileName := range files {
			log.Infof("Extracting %v", idxFileName)
			dirName := idxFileName[:strings.Index(idxFileName, ".")]
			datFileName := dirName + ".dat"
			if _, err := os.Stat(dirName); os.IsNotExist(err) {
				err := os.Mkdir(dirName, 0777)
				checkError(err)
			}
			//Read in the whole idx file so we can determine number of files in archive
			content, err := ioutil.ReadFile(idxFileName)
			checkError(err)
			//Just open a pointer to the dat file so we can read subsections later
			datFile, err := os.Open(datFileName)
			checkError(err)
			defer datFile.Close()
			//There's unknown metadata in the first 11 bytes, so we don't need them
			content = content[11:]
			//Set up our concurrency to wait for the number of files
			sem := make(chan bool, concurrency)
			//indice over the idx file, jumping an entry's number of bytes at a time
			if pre121 && len(content)%oldBytesPerFile == 0 {
				for i := 0; i < len(content); i += oldBytesPerFile {
					//Get our information out of the idx file
					hash := content[i : i+4]
					compressed := binary.LittleEndian.Uint32(content[i+4 : i+8])
					uncompressed := binary.LittleEndian.Uint32(content[i+8 : i+12])
					offset := binary.LittleEndian.Uint32(content[i+12 : i+16])
					isCompressed := content[i+16] == 1
					//Since the files are independent, we can run through them concurrently
					sem <- true
					go func(hash []byte, uncompressed uint32, compressed uint32, offset uint32, isCompressed bool) {
						defer func() { <-sem }()
						fileName := hex.EncodeToString(hash)
						if _, ok := hashToFileName[fileName]; ok {
							fileName = hashToFileName[fileName]
						}
						if _, err := os.Stat(dirName + "/" + fileName); overwrite || err != nil {
							outFile, err := os.Create(dirName + "/" + fileName)
							checkError(err)
							bytesToRead := compressed
							//If the file is compressed, decompress using gzip
							if isCompressed {
								bytesRead := make([]byte, bytesToRead)
								datFile.ReadAt(bytesRead, int64(offset))
								b := bytes.NewReader(bytesRead)
								r, err := gzip.NewReader(b)
								log.Info(fileName)
								if err == nil {
									io.Copy(outFile, r)
									r.Close()
								} else {
									outFile.Write(bytesRead)
								}
							} else { //else just write out
								bytesRead := make([]byte, bytesToRead)
								datFile.ReadAt(bytesRead, int64(offset))
								outFile.Write(bytesRead)
							}
							err = outFile.Close()
							checkError(err)
						}
					}(hash, compressed, uncompressed, offset, isCompressed)
				}
			} else if len(content)%bytesPerFile == 0 {
				for i := 0; i < len(content); i += bytesPerFile {
					//Get our information out of the idx file
					hash := content[i : i+4]
					compressed := binary.LittleEndian.Uint64(content[i+4 : i+12])
					uncompressed := binary.LittleEndian.Uint64(content[i+12 : i+20])
					offset := binary.LittleEndian.Uint64(content[i+20 : i+28])
					isCompressed := content[i+28] == 1
					//Since the files are independent, we can run through them concurrently
					sem <- true
					go func(hash []byte, uncompressed uint64, compressed uint64, offset uint64, isCompressed bool) {
						defer func() { <-sem }()
						fileName := hex.EncodeToString(hash)
						if _, ok := hashToFileName[fileName]; ok {
							fileName = hashToFileName[fileName]
						}
						if _, err := os.Stat(dirName + "/" + fileName); overwrite || err != nil {
							outFile, err := os.Create(dirName + "/" + fileName)
							checkError(err)
							bytesToRead := compressed
							bytesRead := make([]byte, bytesToRead)
							datFile.ReadAt(bytesRead, int64(offset))
							//If the file is compressed, decompress using gzip
							if isCompressed {
								b := bytes.NewReader(bytesRead)
								r, err := gzip.NewReader(b)
								//Some files are 0 or 4 bytes, so they don't uncompress
								if err != nil {
									outFile.Write(bytesRead)
								} else {
									io.Copy(outFile, r)
									r.Close()
								}
							} else { //else just
								outFile.Write(bytesRead)
							}
							err = outFile.Close()
							checkError(err)
						}
					}(hash, compressed, uncompressed, offset, isCompressed)
				}
			} else {
				log.Error("Content size doesn't match data type")

				return
			}
			//wait for our concurrency before finishing
			for i := 0; i < cap(sem); i++ {
				sem <- true
			}
		}

		if !langToJSON {
			log.Info("Converting Lang files to JSON")
			frostlang.ConvertLangToJSON(dir+"/localizations/", overwrite)
		}

		if !dds {
			if !png {
				log.Info("Repairing DDS files and creating PNGs")
				frostexture.ConvertToDDSandPNG(dir+"/textures-s3/", overwrite, true)
			} else {
				log.Info("Repairing DDS files")
				frostexture.ConvertToDDSandPNG(dir+"/textures-s3/", overwrite, false)
			}
		}

		log.Info("Process ran in ", time.Since(startTime))
	}
}

//Simple function to panic if there's an error
func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
