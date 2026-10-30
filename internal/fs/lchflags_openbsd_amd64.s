// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// libc's chflagsat, as golang.org/x/sys/unix calls libc on OpenBSD
// (zsyscall_openbsd_amd64.s there).
TEXT libc_chflagsat_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_chflagsat(SB)
GLOBL	·libc_chflagsat_trampoline_addr(SB), RODATA, $8
DATA	·libc_chflagsat_trampoline_addr(SB)/8, $libc_chflagsat_trampoline<>(SB)
