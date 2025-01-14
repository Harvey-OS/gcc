/* Copyright (C) 2012-2017 Free Software Foundation, Inc.
   Contributed by Richard Henderson <rth@redhat.com>.

   This file is part of the GNU Atomic Library (libatomic).

   Libatomic is free software; you can redistribute it and/or modify it
   under the terms of the GNU General Public License as published by
   the Free Software Foundation; either version 3 of the License, or
   (at your option) any later version.

   Libatomic is distributed in the hope that it will be useful, but WITHOUT ANY
   WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
   FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
   more details.

   Under Section 7 of GPL version 3, you are granted additional
   permissions described in the GCC Runtime Library Exception, version
   3.1, as published by the Free Software Foundation.

   You should have received a copy of the GNU General Public License and
   a copy of the GCC Runtime Library Exception along with this program;
   see the files COPYING3 and COPYING.RUNTIME respectively.  If not, see
   <http://www.gnu.org/licenses/>.  */

#if HAVE_IFUNC
#include <cpuid.h>

extern unsigned int libat_feat1_ecx HIDDEN;
extern unsigned int libat_feat1_edx HIDDEN;

#ifdef __x86_64__
# define IFUNC_COND_1	(libat_feat1_ecx & bit_CMPXCHG16B)
#else
# define IFUNC_COND_1	(libat_feat1_edx & bit_CMPXCHG8B)
#endif

#ifdef __x86_64__
# define IFUNC_NCOND(N) (N == 16)
#else
# define IFUNC_NCOND(N) (N == 8)
#endif

#ifdef __x86_64__
# undef MAYBE_HAVE_ATOMIC_CAS_16
# define MAYBE_HAVE_ATOMIC_CAS_16	IFUNC_COND_1
# undef MAYBE_HAVE_ATOMIC_EXCHANGE_16
# define MAYBE_HAVE_ATOMIC_EXCHANGE_16	IFUNC_COND_1
# undef MAYBE_HAVE_ATOMIC_LDST_16
# define MAYBE_HAVE_ATOMIC_LDST_16	IFUNC_COND_1
# if IFUNC_ALT == 1
#  undef HAVE_ATOMIC_CAS_16
#  define HAVE_ATOMIC_CAS_16 1
# endif
#else
# undef MAYBE_HAVE_ATOMIC_CAS_8
# define MAYBE_HAVE_ATOMIC_CAS_8	IFUNC_COND_1
# undef MAYBE_HAVE_ATOMIC_EXCHANGE_8
# define MAYBE_HAVE_ATOMIC_EXCHANGE_8	IFUNC_COND_1
# undef MAYBE_HAVE_ATOMIC_LDST_8
# define MAYBE_HAVE_ATOMIC_LDST_8	IFUNC_COND_1
# if IFUNC_ALT == 1
#  undef HAVE_ATOMIC_CAS_8
#  define HAVE_ATOMIC_CAS_8 1
# endif
#endif

#endif /* HAVE_IFUNC */

#include_next <host-config.h>
