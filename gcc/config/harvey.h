/* Operating system specific defines to be used when targeting GCC
   for Harvey.

 Copyright (C) 1989, 1992, 1993, 1994, 1995, 1996, 1997, 1998, 1999,
   2000, 2001, 2002, 2003, 2004, 2005, 2007, 2008, 2009, 2010, 2011
   Free Software Foundation, Inc.
   Contributed by Alvaro Jurado <elbingmiss@gmail.com>.

This file is part of GCC.

GCC is free software; you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free
Software Foundation; either version 3, or (at your option) any later
version.

GCC is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or
FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General Public License
for more details.

Under Section 7 of GPL version 3, you are granted additional
permissions described in the GCC Runtime Library Exception, version
3.1, as published by the Free Software Foundation.

You should have received a copy of the GNU General Public License and
a copy of the GCC Runtime Library Exception along with this program;
see the files COPYING3 and COPYING.RUNTIME respectively.  If not, see
<http://www.gnu.org/licenses/>.  */


/* String containing the assembler's comment-starter.
 * Note the trailing space is necessary in case the character
 * that immediately follows the comment is '*'.  If this happens
 * and the space is not there the assembler will interpret this
 * as the start of a C-like slash-star comment and complain when
 * there is no terminator.
 * Overriding gas.h, from unix.h.
 */

#undef ASM_COMMENT_START
#define ASM_COMMENT_START "/ "

/* No separate math library (no -lm). It's in apex/libap/math :) */

#define MATH_LIBRARY ""

/*                          Harvey POSIX threads                     *
 *                          *******************                      */

/* Ensuring there's not native mode and get APEX startfiles */

#undef STARTFILE_SPEC
#define STARTFILE_SPEC \
 "%{!shared:
    %{native: }
    %{!native: crt1.o%s crti.o%s }
  }"

/* Ensuring there's not native mode and get APEX endfile */

#undef ENDFILE_SPEC
#define ENDFILE_SPEC \
  "%{!shared:
    %{native: }
    %{!native: crtn.o%s }
  }"

/* Added libbsd and libpthread as a part of standard library, both are
 * used by default. Check for shared because there are dummy shared flags
 * during bootstrap or into another packages of sources made with
 * autoconf. Check for native too, because native threads are not implemented
 * here yet.
 */

#undef LIB_SPEC
#ifdef HARVEY_NO_THREADS
# define LIB_SPEC "         \
  %{pthread: %eThe -pthread option is only supported on Harvey when gcc \
is built with the --enable-threads configure-time option.}    \
  %{!shared:                 \
    %{!native:               \
      %{!pg: -lap -lc} \
    }                        \
    %{native:                \
      %{!pg: -e_main -lc}    \
    }                        \
  }"
#else
# define LIB_SPEC "          \
  %{!shared:                 \
    %{!native:               \
      %{!pg: %{pthread:-lpthread} -lap -lc} \
    }                        \
    %{native:                \
      %{!pg: -e_main -lc}    \
    }                        \
  }"
#endif

/* Every program in Harvey compiled with GCC links against libpthread.a
 * which contains the pthread routines, so there's no need to explicitly
 * call out when doing threaded work (-fopenmp / -fgnu-tm options imply
 * pthreads in gcc.c).
 */

#undef GOMP_SELF_SPECS
#define GOMP_SELF_SPECS ""
#undef GTM_SELF_SPECS
#define GTM_SELF_SPECS ""

/* Need it by default unless you want to type always -pthread option; libgcc,
 * gfortran, g++ and others libs will use it by default. Specified in
 * harvey.opt file. Default option removed except for AIX ad Solaris
 * since GCC 4.6. Perhaps more options would be added to that file.
 */

//#define DRIVER_SELF_SPECS "-pthread"

/*                          END Harvey POSIX threads                 *
 *                          ***********************                  */

/*                            Harvey C++                             *
 *                            *********                              */

/* Harvey linking with libstdc++ requires libsupc++ as well. */

#define LIBSTDCXX_STATIC "supc++"

/*                          END Harvey C++                           *
 *                          *************                            */

/****************
 * EXPERIMENTAL *
 ****************/

/* We need this since G++ is the default compiler. Avoiding possible
 * issues about C++ system C headers compliance.
 */

#undef NO_IMPLICIT_EXTERN_C
#define NO_IMPLICIT_EXTERN_C

/* Names to predefine in the preprocessor for this target machine.  */

#undef TARGET_OS_CPP_BUILTINS
#define TARGET_OS_CPP_BUILTINS() \
  do              \
    {             \
  builtin_define ("HARVEY");         \
  builtin_define ("__HARVEY__");         \
  builtin_define ("__LITTLE_ENDIAN__");   \
  builtin_define ("_POSIX_SOURCE");     \
  builtin_define ("_LIMITS_EXTENSION");   \
  builtin_define ("_BSD_EXTENSION");      \
  builtin_define ("_BSD_SOURCE");      \
  builtin_define ("_SUSV2_SOURCE");     \
  builtin_define ("_RESEARCH_SOURCE");    \
  builtin_assert ("system=harvey");      \
  builtin_assert ("system=unix");      \
  builtin_assert ("system=posix");      \
    }             \
  while (0

/* If hosted on Harvey, set standard directory paths accordingly.
 * These definitions are target-specific.
 */

#define CPP_SPEC "%{native: -I/sys/include -I/amd64/include}"

/* Looking for right place for c++ headers. Horrible hack, it should be
 * specified at configure time.
 */

#undef GPLUSPLUS_INCLUDE_DIR
#define GPLUSPLUS_INCLUDE_DIR "/apex/ports/cross2/include/c++/4.7.3"

#undef LOCAL_INCLUDE_DIR
#define LOCAL_INCLUDE_DIR "/apex/cross2/include"

#undef NATIVE_SYSTEM_HEADER_DIR
#define NATIVE_SYSTEM_HEADER_DIR "/apex/amd64/include"

#undef STANDARD_STARTFILE_PREFIX
#define STANDARD_STARTFILE_PREFIX "/apex/amd64/lib/"

/* The host-specific ones are here */

#undef STANDARD_EXEC_PREFIX
#define STANDARD_EXEC_PREFIX "/apex/ports/cross2/bin/"

#undef STANDARD_BINDIR_PREFIX
#define STANDARD_BINDIR_PREFIX "/apex/ports/cross2/bin/"

#define MD_EXEC_PREFIX    "/apex/ports/cross2/bin/"

#undef TOOLDIR_BASE_PREFIX
#define TOOLDIR_BASE_PREFIX "../../../../apex/ports/cross2/"
