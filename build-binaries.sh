#!/bin/sh
fyne-cross linux -tags migrated_fynedo -name imgview ./cmd/imgview
fyne-cross linux -tags migrated_fynedo -name tieview ./cmd/tieview
fyne-cross windows -tags migrated_fynedo -name imgview ./cmd/imgview
fyne-cross windows -tags migrated_fynedo -name tieview ./cmd/tieview
tie-upload fyne-cross/bin/linux-amd64/imgview -server tfh1.com
tie-upload fyne-cross/bin/linux-amd64/tieview -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/imgview.exe -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/tieview.exe -server tfh1.com
