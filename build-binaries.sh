#!/bin/sh
fyne-cross linux
fyne-cross windows
tie-upload fyne-cross/bin/linux-amd64/imgview -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/imgview.exe -server tfh1.com
