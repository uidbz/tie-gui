### Simple image viewer with gallery feature

The gallery positions the images with consideration to landscape or portrait orientation.  
I like it. I think it looks cool, and gives a good overview of the pictures.  
Next step will be to do something with the background/white space.  

In addition to this, it loads thumbnails with 8 Go routines, so it is pretty fast to load :-)  
Zoom and reposition is rather slow though :-/  

Hotkeys/controls:  
In gallery mode:  
Q: Quit  
Esc: Quit  
Down/J: Scroll down  
Up/K: Scroll up  

In single picture mode:   
Esc: Go back to gallery  
Right/J/Space: Next picture  
Left/K: Previous picture  
Up: Rotate left  
Down: Rotate right  
Scroll down: Zoom in  
Scroll up: Zoom out  
Drag image to reposition  
X: Fit to window  
S: Zoom to original size  
F: Fullscreen (fails on wayland)  
