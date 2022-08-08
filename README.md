# frostract

frostract is a utility for extracting files from Frostpunk idx and dat archives. Just put the built executable in the same directory as your .dat and .idx files that you want to extract, and it'll do the rest.

Usage: frostract [flags]

Flags:

 -h		Display this help

 -d		Skip repairing dds files. Setting this flag will also skip conversion to png

 -p		Skip converting dds to png

 -j		Skip converting lang files to json

 -o		Overwrite existing files
 
 -v		Idx is from before version 1.2.1

 -c     Instead of extracting, compress existing subdirectories and idx files into new idx and dat files

## Build

go get

go install

## Caveats

* Png conversion requires [ImageMagick](https://imagemagick.org/script/command-line-processing.php) utility.
* FPHook.log must be in same directory for filename lookup.

## License

[MIT](LICENSE)
