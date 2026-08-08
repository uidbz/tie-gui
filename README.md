# Simple image viewer with gallery feature + archive support

Website: https://imgview.app

The gallery uses a justified row layout: images are grouped into rows and scaled so each row fills the full window width with no gaps. Portrait and landscape images naturally share a row at the same height, sized by their aspect ratio. The tile size is controlled by `TileWidth` in the config (default 300 px — the target row height).

It can read most common archive formats - such as zip, rar, tar, cbr, etc.

It loads relatively fast on my computer, but zoom and reposition is unfortunatly rather slow on high resolution pictures :-/  

## Download precompiled binary
#### Latest build: 2022-07-09
* Imgview for Linux X11: [Download](https://tfh1.com/38ca24392a20f04457572a7dbceb2380e6f824200329be4a58f5e302493baa1f/imgview)
* Imgview for Windows: [Download](https://tfh1.com/7074246ba84ae7ff1510968fe23183b4e46c101a9e1a87dcb56c62a3e160ed0f/imgview.exe) (Windows is untested)
* I do not provide binaries for Apple Operating systems, since I do not agree with the Xcode license terms. That said, imgview probably works if you compile it yourself.

Just download and remember to make it executable.

## TODO (report bugs and wishes)
[Here](https://todo.sr.ht/~uid/imgview)

## Build 
Imgview is written in Go, and made using the excellent [fyne](https://fyne.io) GUI framework, which in turn depends on cgo - see details [here](https://developer.fyne.io/started)  
When you have a C compiler and the dependencies of fyne installed, then compiling imgview is very straight forward.  
~~~sh
git clone https://git.sr.ht/~uid/imgview
cd imgview
go build ./cmd/imgview # -tags wayland # for building on wayland
go build ./cmd/tieview  # the tie-backed variant
~~~
Or alternatively
~~~sh
go install git.sr.ht/~uid/imgview/cmd/imgview
go install git.sr.ht/~uid/imgview/cmd/tieview
~~~

## Repository layout
* `gallery/` - the generic fyne image-viewing/gallery component shared by both apps (no tie dependency)
* `cmd/imgview/` - the local-files image viewer (dirs and archives)
* `cmd/tieview/` - the [tie](https://git.sr.ht/~uid/tie)-backed image viewer (tag queries and virtual-filesystem navigation, filehost-cached thumbnails)
* `tagselection/` - the tag picker widget used by tieview

## Run
~~~sh
imgview /path/to/img/or/dir/or/archive
tieview -tag favorite
# tieview flags:
#   -c/-config other.toml   tie config file (a name searched in tie's config dirs, or a file path)
#   -host default           fetch content from this filehost named in the tie config
~~~

## Default Key Bindings
Change by copying [config.toml](https://git.sr.ht/~uid/imgview/tree/master/item/gallery/config.toml) to:  
Linux: ~/.config/imgview/config.toml  
Windows: %AppData%\imgview\config.toml   
and then edit to your liking :-)

### In gallery mode
| Key | Action |
| --------- | --------------------- |
| Q | Quit |
| Esc | Quit |
| Down/J | Scroll down |
| Up/K | Scroll up |

### In single picture mode
| Key | Action |
| --------- | --------------------- |
| Esc | Go back to gallery |
| Right/J | Next picture | 
| Left/K | Previous picture | 
| Up/H | Rotate left | 
| Down/L | Rotate right | 
| X | Fit to window |
| B | Switch filtering mode |
| S | Zoom to original size | 
| F | Fullscreen (fails on wayland) | 

| Mouse | Action |
| --------- | --------------------- |
| Scroll down| Zoom in | 
| Scroll up | Zoom out | 
| Drag | Reposition image | 
