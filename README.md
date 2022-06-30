# Simple image viewer with gallery feature + archive support

The gallery positions the images with consideration to landscape or portrait orientation.  
I like it. I think it gives a good overview of the pictures.  
Next step will be to do something with the background/white space.  

It can read most common archive formats - such as zip, rar, tar, cbr, etc.

It loads relatively fast on my computer, but zoom and reposition is unfortunatly rather slow on high resolution pictures :-/  

## Download precompiled binary
* 2022-06-30 - Imgview for Linux X11: [Download](https://tfh1.com/c9038d72d514ee3b936550bfd75d37d7b9dcb212024bb897afc038b4fc0553f1/imgview)
* 2022-06-30 - Imgview for Windows: [Download](https://tfh1.com/70d4f601b121aefd49ab28248d730a3bd8f5354eb46c67eb34d298532cd83333/imgview.exe) (Windows is untested)
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
go build # -tags wayland # for building on wayland
go install # to install to $GOPATH/bin
~~~
Or alternatively
~~~sh
go install git.sr.ht/~uid/imgview
~~~

## Run
~~~sh
imgview /path/to/img/or/dir/or/archive
~~~

## Default Key Bindings
Change by copying [config.toml](https://git.sr.ht/~uid/imgview/tree/master/item/config.toml) to:  
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
