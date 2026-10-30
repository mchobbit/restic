// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build illumos && gc

#include "textflag.h"

// Calls into libc go through the runtime, as golang.org/x/sys/unix's do
// (asm_solaris_amd64.s there); attrs_illumos.go uses it for acl(2).
TEXT ·sysvicall6(SB),NOSPLIT,$0-88
	JMP	syscall·sysvicall6(SB)
