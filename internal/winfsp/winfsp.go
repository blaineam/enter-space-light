// Package winfsp detects the WinFsp driver that Windows mounts need.
//
// Enter Space Light uses WinFsp under its FLOSS exception, which requires
// this notice in the user interface and user-facing documentation:
//
//	WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos
//	https://github.com/winfsp/winfsp
package winfsp

// Notice is the attribution WinFsp's FLOSS exception requires us to show.
const Notice = "WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos"

// URL is the WinFsp repository, shown next to Notice.
const URL = "https://github.com/winfsp/winfsp"

// WingetID installs WinFsp from the Windows Package Manager.
const WingetID = "WinFsp.WinFsp"
