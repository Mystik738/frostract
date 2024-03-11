package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Mystik738/frostexture"
	"github.com/Mystik738/frostlang"
	"github.com/aviddiviner/go-murmur"

	log "github.com/sirupsen/logrus"
)

const bytesPerFile = 29
const oldBytesPerFile = 17
const fphookFileName = "FPHook.log"

func main() {
	log.SetLevel(log.InfoLevel)

	overwrite := flag.Bool("o", false, "Overwrite existing files")
	help := flag.Bool("h", false, "Display this help")
	dds := flag.Bool("d", false, "Skip repairing dds files. Setting this flag will also skip conversion to png")
	png := flag.Bool("p", false, "Skip converting dds to png")
	pre121 := flag.Bool("v", false, "Idx is from before version 1.2.1")
	langToJSON := flag.Bool("j", false, "Skip converting lang files to json")
	compress := flag.Bool("c", false, "Instead of extracting, compress existing subdirectories and idx files into new idx and dat files")
	files := flag.String("f", "", "File(s) to extract or directories to compress")

	flag.Parse()

	if !*compress {
		Frostract(*overwrite, *help, *dds, *png, *pre121, *langToJSON, *files)
	} else {
		Frostpress(*overwrite, *pre121, *files)
	}
}

// Frostpress compresses a directory into a dat and .idx file
func Frostpress(overwrite, pre121 bool, filespec string) {
	parentDir, err := os.Getwd()
	checkError(err)

	//Get all directories that are children of the current directory
	if filespec == "" {
		filespec = "*"
	}
	files, err := filepath.Glob(filespec)
	checkError(err)
	var dirs []string
	for _, file := range files {
		info, err := os.Stat(file)
		checkError(err)
		if info.IsDir() {
			dirs = append(dirs, file)
			log.Debugf("Adding %v", info.Name())
		}
	}

	//get our hashes
	hashToFileName := readFPHook(true)

	//For each directory, compress
	for _, dir := range dirs {
		//If there are json files, convert them back to the lang files
		frostlang.ConvertJSONToLang(dir, overwrite)
		var files []string
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if info.IsDir() {
				return nil
			}
			//We can ignore the custom json, dds, and png files that this utility writes
			if filepath.Ext(path) == ".json" || filepath.Ext(path) == ".dds" || filepath.Ext(path) == ".png" {
				return nil
			}

			files = append(files, info.Name())
			return nil
		})

		log.Infof("Compressing %v directory", dir)
		idxFileName := filepath.Join(parentDir, dir+".idx")
		datName := filepath.Join(parentDir, dir+".dat")

		//We should keep the dat file in the same order as the original, but do we need to?
		content, err := os.ReadFile(idxFileName)
		if err != nil {
			log.Infof("%v does not exist, skipping directory %v", idxFileName, dir)
			continue
		}

		//We don't want to overwrite existing files unless the overwrite flag is set
		if _, err := os.Stat(idxFileName); overwrite || err != nil {

		} else {
			log.Fatalf("%v already exists, must flag overwrite", idxFileName)
		}
		if _, err := os.Stat(datName); overwrite || err != nil {
			fh, err := os.Create(datName)
			checkError(err)
			fh.Close()
		} else {
			log.Fatalf("%v already exists", datName)
		}

		content = content[11:]
		offsetToHash := make(map[uint64]string)
		for i := 0; i < len(content); i += bytesPerFile {
			//Get our information out of the idx file
			hash := content[i : i+4]
			offsetToHash[binary.LittleEndian.Uint64(content[i+20:i+28])] = hex.EncodeToString(hash)
		}
		//log.Debugf("OffsetsToHash: %v", offsetToHash)

		offsetOrder := make(map[uint64]string, 0)
		offsets := make([]uint64, 0)
		//Need to find original dat order
		for i := 0; i < len(content); i += bytesPerFile {
			hash := hex.EncodeToString(content[i : i+4])
			offset := binary.LittleEndian.Uint64(content[i+20 : i+28])

			offsets = append(offsets, offset)
			offsetOrder[offset] = hash
		}

		sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
		//log.Debugf("Offsets: %v", offsets)
		//log.Debugf("OffsetsOrder: %v", offsetOrder)
		orderedHashes := make([][]byte, 0)

		for _, offset := range offsets {
			decoded, err := hex.DecodeString(offsetOrder[offset])
			checkError(err)
			orderedHashes = append(orderedHashes, decoded)
		}

		offset := 0

		hashInfo := make(map[string][]byte, 0)

		log.Debugf("There are %d hashes in the list", len(orderedHashes))

		for _, hash := range orderedHashes {
			file, ok := hashToFileName[hex.EncodeToString(hash)]
			if !ok {
				file = hex.EncodeToString(hash)
			}

			fileName := filepath.Join(parentDir, dir, file)
			tmpFileName := filepath.Join(parentDir, dir, file+".gz")

			info, err := os.Stat(datName)
			checkError(err)

			//Write to a temporary gz file. Without this, gzip doesn't close the file correctly.
			data, err := os.ReadFile(fileName)
			checkError(err)
			tmp, err := os.OpenFile(tmpFileName, os.O_WRONLY|os.O_CREATE, 0660)
			checkError(err)
			gzdat, err := gzip.NewWriterLevel(tmp, gzip.BestCompression)
			checkError(err)
			gzdat.Header.OS = byte(0)
			fw := bufio.NewWriter(gzdat)
			fw.Write(data)
			uncompressed := len(data)
			fw.Flush()
			gzdat.Close()
			tmp.Close()

			//Read from our temp file and move the data into the .dat
			data, err = os.ReadFile(tmpFileName)
			checkError(err)
			dat, err := os.OpenFile(datName, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0660)
			checkError(err)
			datSizeBefore := info.Size()
			//Frostpunk doesn't have checksum and size footer in the dat file
			dat.Write(data[:len(data)-8])
			dat.Close()
			os.Remove(tmpFileName)

			//Get our compressed size
			datinfo, err := os.Stat(datName)
			checkError(err)
			compressed := datinfo.Size() - datSizeBefore

			//store the file information in a map
			stringHash := hex.EncodeToString(hash)
			//log.Debugf("%v %v %v %v\n", stringHash, offset, compressed, uncompressed)
			writeByte := hash
			hashInfo[stringHash] = writeByte
			writeCompressed := make([]byte, 8)
			binary.LittleEndian.PutUint64(writeCompressed, uint64(compressed))
			hashInfo[stringHash] = append(hashInfo[stringHash], writeCompressed...)
			writeUncompressed := make([]byte, 8)
			binary.LittleEndian.PutUint64(writeUncompressed, uint64(uncompressed))
			hashInfo[stringHash] = append(hashInfo[stringHash], writeUncompressed...)
			writeOffset := make([]byte, 8)
			binary.LittleEndian.PutUint64(writeOffset, uint64(offset))
			hashInfo[stringHash] = append(hashInfo[stringHash], writeOffset...)

			offset = int(datinfo.Size())
		}

		log.Infof("Compressed %d files", len(orderedHashes))

		//idx written in original order
		idx, err := os.Create(idxFileName)
		checkError(err)
		defer idx.Close()

		//write our header information
		idxHeader := []byte{0x00, 0x02, 0x01}
		checkError(err)
		idx.Write(idxHeader)
		headerContent := make([]byte, 8)
		binary.LittleEndian.PutUint64(headerContent, uint64(len(content)/bytesPerFile))
		idx.Write(headerContent)

		for i := 0; i < len(content); i += bytesPerFile {
			hash := hex.EncodeToString(content[i : i+4])
			isCompressed := content[i+28]
			hashInfo[hash] = append(hashInfo[hash], isCompressed)
			idx.Write(hashInfo[hash])
			log.Debugf("%v\n", hex.EncodeToString(hashInfo[hash]))
		}

		log.Infof("Indexed %d files", len(content)/bytesPerFile)
	}
}

func Frostract(overwrite, help, dds, png, pre121, langToJSON bool, filespec string) {
	if help {
		helptext := `frostract is a utility for extracting files from Frostpunk idx and dat archives. Requires magick to be installed to convert dds to png. FPHook.log must be in same directory for filename lookup.
Usage: frostract [flags] [options]

Flags:
 -h		Display this help
 -d		Skip repairing dds files. Setting this flag will also skip conversion to png
 -p		Skip converting dds to png
 -j		Skip converting lang files to json
 -o		Overwrite existing files
 -v		Idx is from before version 1.2.1
 -c     Instead of extracting, compress existing subdirectories and idx files into new idx and dat files

Options:
 -f <filespec>     Extract specific idx files or compress specific directories
`
		log.Info(helptext)
	} else {
		startTime := time.Now()
		dir, err := os.Getwd()
		//This is more a heuristic value to increase efficiency rather than an exact science
		concurrency := 8
		checkError(err)
		log.Debugf("Working directory is %v", dir)
		//Make our hashmap
		hashToFileName := readFPHook(true)

		//Walk our current path to get idx files
		if filespec == "" {
			filespec = "*.idx"
		}
		files, err := filepath.Glob(filespec)
		checkError(err)
		var matchingFiles []string
		for _, file := range files {
			info, err := os.Stat(file)
			checkError(err)
			if !info.IsDir() && filepath.Ext(file) == ".idx" {
				log.Debugf("Adding %v", file)
				matchingFiles = append(matchingFiles, file)
			}
		}
		for _, idxFileName := range matchingFiles {
			log.Infof("Extracting %v", idxFileName)
			parentDirName := filepath.Dir(idxFileName)
			baseName := filepath.Base(idxFileName)
			stemName := baseName[:len(baseName)-len(filepath.Ext(baseName))]
			datFileName := filepath.Join(parentDirName, stemName+".dat")
			if _, err := os.Stat(stemName); os.IsNotExist(err) {
				wdirStat, err := os.Stat(dir)
				checkError(err)
				err = os.Mkdir(stemName, wdirStat.Mode())
				checkError(err)
			}
			//Read in the whole idx file so we can determine number of files in archive
			content, err := os.ReadFile(idxFileName)
			checkError(err)
			//Just open a pointer to the dat file so we can read subsections later
			datFile, err := os.Open(datFileName)
			checkError(err)
			defer datFile.Close()
			//Metadata
			//All .idx files start with 00 02 01
			//Then uint64 for number of files
			log.Infof("%v has %v files", idxFileName, binary.LittleEndian.Uint64(content[3:11]))
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
						filePath := filepath.Join(stemName, fileName)
						if _, err := os.Stat(filePath); overwrite || err != nil {
							dirPath := filepath.Dir(filePath)
							err := os.MkdirAll(dirPath, os.ModePerm)
							checkError(err)
							outFile, err := os.Create(filePath)
							checkError(err)
							bytesToRead := compressed
							//If the file is compressed, decompress using gzip
							if isCompressed {
								bytesRead := make([]byte, bytesToRead)
								datFile.ReadAt(bytesRead, int64(offset))
								b := bytes.NewReader(bytesRead)
								r, err := gzip.NewReader(b)
								log.Debug(fileName)
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

					log.Debugf("%v %v %v %v %v\n", hex.EncodeToString(hash), offset, compressed, uncompressed, isCompressed)

					//Since the files are independent, we can run through them concurrently
					sem <- true
					go func(hash []byte, uncompressed uint64, compressed uint64, offset uint64, isCompressed bool) {
						defer func() { <-sem }()
						fileName := hex.EncodeToString(hash)
						if _, ok := hashToFileName[fileName]; ok {
							fileName = hashToFileName[fileName]
						}
						filePath := filepath.Join(stemName, fileName)
						if _, err := os.Stat(filePath); overwrite || err != nil {
							dirPath := filepath.Dir(filePath)
							err := os.MkdirAll(dirPath, os.ModePerm)
							checkError(err)
							outFile, err := os.Create(filePath)
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

			log.Debugf("Extracted %d files", len(content)/bytesPerFile)
		}

		if !langToJSON {
			log.Info("Converting Lang files to JSON")
			frostlang.ConvertLangToJSON(dir+"/localizations/", overwrite)
		}

		if !dds {
			createPng := true
			if !png {
				log.Info("Repairing DDS files and creating PNGs")
			} else {
				log.Info("Repairing DDS files")
				createPng = false
			}
			if _, err := os.Stat(dir + "/textures-s3/"); err == nil {
				err := filepath.WalkDir(dir+"/textures-s3/", func(path string, d fs.DirEntry, err error) error {
					if d.IsDir() {
						frostexture.ConvertToDDSandPNG(path, overwrite, createPng)
					}
					return nil
				})
				checkError(err)
			}
		}

		log.Info("Process ran in ", time.Since(startTime))
	}
}

// Simple function to panic if there's an error
func checkError(err error) {
	if err != nil {
		log.Panic(err)
		panic(err)
	}
}

// Read in FPHook to a map
func readFPHook(hashToFile bool) map[string]string {
	var err error
	dir, err := os.Getwd()
	checkError(err)
	// first, look for FPHook in the working directory
	fphookFilePath := filepath.Join(dir, fphookFileName)
	_, err = os.Stat(fphookFilePath)
	if os.IsNotExist(err) {
		// next, look for FPHook in the directory with the frostract binary
		// TODO handle symlinks
		var execFileName string
		execFileName, err = os.Executable()
		checkError(err)
		fphookFilePath = filepath.Join(filepath.Dir(execFileName), fphookFileName)
		_, err = os.Stat(fphookFilePath)
	}

	fpmap := make(map[string]string)
	lineCounts := make(map[string]int)
	if !os.IsNotExist(err) {
		//Get our FPHook file to get hashes
		f, err := os.Open(fphookFilePath)
		checkError(err)
		r := bufio.NewReader(f)
		lineCount := 0
		for {
			line, isPrefix, err := r.ReadLine()
			lineCount++
			if err != nil || isPrefix {
				break
			}
			fileName := string(line)
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, murmur.MurmurHash2([]byte(line), 0))
			if hashToFile {
				_, ok := fpmap[hex.EncodeToString(b)]
				if !ok {
					fpmap[hex.EncodeToString(b)] = fileName
				} else {
					log.Warnf("Duplicate hash %v and %v", string(line), fpmap[hex.EncodeToString(b)])
				}
			} else {
				fpmap[fileName] = hex.EncodeToString(b)
			}
			if _, ok := lineCounts[string(line)]; ok {
				lineCounts[string(line)]++
			} else {
				lineCounts[string(line)] = 1
			}
		}
		f.Close()
		//This is just to trim out duplicates, maybe flag to turn off
		if len(lineCounts) < lineCount {
			// TODO handle FPHook file is not writable
			f, err = os.Create(filepath.Join(dir, fphookFileName))
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

		f.Close()
	} else {
		log.Warn("FPHook.log does not exist in current directory, cannot adjust filenames")
	}

	return fpmap
}
