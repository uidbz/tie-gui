# Simple image viewer with gallery feature + archive support

The gallery positions the images with consideration to landscape or portrait orientation.  
I like it; I think it gives a good overview of the pictures.  
Next step will be to do something with the background/white space.  

It can read most common archive formats - such as zip, rar, tar, cbr, etc.

It loads relatively fast on my computer, but zoom and reposition is unfortunatly rather slow on high resolution pictures :-/  

# Build 
Imgview is written in Go, and made using the excellent [fyne](https://fyne.io) GUI framework, which in turn depends on cgo - see details [here](https://developer.fyne.io/started)  
When you have a C compiler and the dependencies of fyne installed, then compiling imgview is very straight forward.  
`git clone https://git.sr.ht/~uid/imgview
cd imgview
go build # -tags wayland # for building on wayland
go install # to install to $GOPATH/bin - you can add this directory to your PATH to make it available system wide
# or alternatively
go install git.sr.ht/~uid/imgview
`
# Run
`imgview /path/to/img/or/dir/or/archive
# optionally set a size of the window
imgview /path/to/img/or/dir/or/archive 1024 768
`
## Hotkeys/controls

### In gallery mode
| Key | Action |
| ----------- | ----------- |
| Q | Quit |
| Esc | Quit |
| Down/J | Scroll down |
| Up/K | Scroll up |

### In single picture mode
| Key | Action |
| ----------- | ----------- |
| Esc | Go back to gallery |
| Right/J/Space | Next picture | 
| Left/K | Previous picture | 
| Up/H | Rotate left | 
| Down/L | Rotate right | 
| X | Fit to window |
| S | Zoom to original size | 
| F | Fullscreen (fails on wayland) | 

| Mouse | Action |
| ----------- | ----------- |
| Scroll down| Zoom in | 
| Scroll up | Zoom out | 
| Drag | Reposition image | 
