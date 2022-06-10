# Simple image viewer with gallery feature + archive support

The gallery positions the images with consideration to landscape or portrait orientation.  
I like it; I think it gives a good overview of the pictures.  
Next step will be to do something with the background/white space.  

It can read most common archive formats - such as zip, rar, tar, cbr, etc.

It loads relatively fast on my computer, but zoom and reposition is unfortunatly rather slow on high resolution pictures :-/  

# Download precompiled binary
* 2022-06-10 - Imgview for Linux X11: [Download](https://tfh1.com/b6cf2666b0cccdfa029cdbc0a1ac4415fe1a9dd4fc64fd40d2942eac73a51733/imgview)
* 2022-06-10 - Imgview for Windows: [Download](https://tfh1.com/a5e3ab4a84b2b83fa918fc6d6979acd5e90f7e6a91c77f568432d2e97f226c8e/imgview.exe)

Just download and remember to make it executable.

# TODO (report bugs and wishes)
[Here](https://todo.sr.ht/~uid/imgview)

# Build 
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

# Run
~~~sh
imgview /path/to/img/or/dir/or/archive
# optionally set a size of the window
imgview /path/to/img/or/dir/or/archive 1024 768
~~~

## Default Key Bindings
Change by copying config.toml to:  
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
