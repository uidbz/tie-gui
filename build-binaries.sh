#!/bin/sh
fyne-cross linux -tags migrated_fynedo -name imgview ./cmd/imgview
fyne-cross linux -tags migrated_fynedo -name tie-view ./cmd/tie-view
fyne-cross windows -tags migrated_fynedo -name imgview ./cmd/imgview
fyne-cross windows -tags migrated_fynedo -name tie-view ./cmd/tie-view
tie-upload fyne-cross/bin/linux-amd64/imgview -server tfh1.com
tie-upload fyne-cross/bin/linux-amd64/tie-view -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/imgview.exe -server tfh1.com
tie-upload fyne-cross/bin/windows-amd64/tie-view.exe -server tfh1.com
