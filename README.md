# frostract

frostract is a utility for extracting files from Frostpunk idx and dat archives. 

Usage: frostract [flags]

Flags:
 -h		Display this help
 -d		Skip repairing dds files. Setting this flag will also skip conversion to png
 -p		Skip converting dds to png
 -j		Skip converting lang files to json
 -o		Overwrite existing files
 -v		Idx is from before version 1.2.1

## Build

go get
go install

## Caveats

* Png conversion requires [ImageMagick](https://imagemagick.org/script/command-line-processing.php) utility.
* FPHook.log must be in same directory for filename lookup.

## License

[MIT](LICENSE)