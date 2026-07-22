#!/bin/sh
fyne-cross linux -tags migrated_fynedo
fyne-cross windows -tags migrated_fynedo
tie-upload fyne-cross/bin/linux-amd64/imgview -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/imgview.exe -server tfh1.com
