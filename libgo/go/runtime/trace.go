// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Go execution tracer.
// The tracer captures a wide range of execution events like goroutine
// creation/blocking/unblocking, syscall enter/exit/block, GC-related events,
// changes of heap size, processor start/stop, etc and writes them to a buffer
// in a compact form. A precise nanosecond-precision timestamp and a stack
// trace is captured for most events.
// See https://golang.org/s/go15trace for more info.

package runtime

import (
	"runtime/internal/sys"
	"unsafe"
)

// Event types in the trace, args are given in square brackets.
const (
	traceEvNone           = 0  // unused
	traceEvBatch          = 1  // start of per-P batch of events [pid, timestamp]
	traceEvFrequency      = 2  // contains tracer timer frequency [frequency (ticks per second)]
	traceEvStack          = 3  // stack [stack id, number of PCs, array of {PC, func string ID, file string ID, line}]
	traceEvGomaxprocs     = 4  // current value of GOMAXPROCS [timestamp, GOMAXPROCS, stack id]
	traceEvProcStart      = 5  // start of P [timestamp, thread id]
	traceEvProcStop       = 6  // stop of P [timestamp]
	traceEvGCStart        = 7  // GC start [timestamp, seq, stack id]
	traceEvGCDone         = 8  // GC done [timestamp]
	traceEvGCScanStart    = 9  // GC scan start [timestamp]
	traceEvGCScanDone     = 10 // GC scan done [timestamp]
	traceEvGCSweepStart   = 11 // GC sweep start [timestamp, stack id]
	traceEvGCSweepDone    = 12 // GC sweep done [timestamp]
	traceEvGoCreate       = 13 // goroutine creation [timestamp, new goroutine id, new stack id, stack id]
	traceEvGoStart        = 14 // goroutine starts running [timestamp, goroutine id, seq]
	traceEvGoEnd          = 15 // goroutine ends [timestamp]
	traceEvGoStop         = 16 // goroutine stops (like in select{}) [timestamp, stack]
	traceEvGoSched        = 17 // goroutine calls Gosched [timestamp, stack]
	traceEvGoPreempt      = 18 // goroutine is preempted [timestamp, stack]
	traceEvGoSleep        = 19 // goroutine calls Sleep [timestamp, stack]
	traceEvGoBlock        = 20 // goroutine blocks [timestamp, stack]
	traceEvGoUnblock      = 21 // goroutine is unblocked [timestamp, goroutine id, seq, stack]
	traceEvGoBlockSend    = 22 // goroutine blocks on chan send [timestamp, stack]
	traceEvGoBlockRecv    = 23 // goroutine blocks on chan recv [timestamp, stack]
	traceEvGoBlockSelect  = 24 // goroutine blocks on select [timestamp, stack]
	traceEvGoBlockSync    = 25 // goroutine blocks on Mutex/RWMutex [timestamp, stack]
	traceEvGoBlockCond    = 26 // goroutine blocks on Cond [timestamp, stack]
	traceEvGoBlockNet     = 27 // goroutine blocks on network [timestamp, stack]
	traceEvGoSysCall      = 28 // syscall enter [timestamp, stack]
	traceEvGoSysExit      = 29 // syscall exit [timestamp, goroutine id, seq, real timestamp]
	traceEvGoSysBlock     = 30 // syscall blocks [timestamp]
	traceEvGoWaiting      = 31 // denotes that goroutine is blocked when tracing starts [timestamp, goroutine id]
	traceEvGoInSyscall    = 32 // denotes that goroutine is in syscall when tracing starts [timestamp, goroutine id]
	traceEvHeapAlloc      = 33 // memstats.heap_live change [timestamp, heap_alloc]
	traceEvNextGC         = 34 // memstats.next_gc change [timestamp, next_gc]
	traceEvTimerGoroutine = 35 // denotes timer goroutine [timer goroutine id]
	traceEvFutileWakeup   = 36 // denotes that the previous wakeup of this goroutine was futile [timestamp]
	traceEvString         = 37 // string dictionary entry [ID, length, string]
	traceEvGoStartLocal   = 38 // goroutine starts running on the same P as the last event [timestamp, goroutine id]
	traceEvGoUnblockLocal = 39 // goroutine is unblocked on the same P as the last event [timestamp, goroutine id, stack]
	traceEvGoSysExitLocal = 40 // syscall exit on the same P as the last event [timestamp, goroutine id, real timestamp]
	traceEvCount          = 41
)

const (
	// Timestamps in trace are cputicks/traceTickDiv.
	// This makes absolute values of timestamp diffs smaller,
	// and so they are encoded in less number of bytes.
	// 64 on x86 is somewhat arbitrary (one tick is ~20ns on a 3GHz machine).
	// The suggested increment frequency for PowerPC's time base register is
	// 512 MHz according to Power ISA v2.07 section 6.2, so we use 16 on ppc64
	// and ppc64le.
	// Tracing won't work reliably for architectures where cputicks is emulated
	// by nanotime, so the value doesn't matter for those architectures.
	traceTickDiv = 16 + 48*(sys.Goarch386|sys.GoarchAmd64|sys.GoarchAmd64p32)
	// Maximum number of PCs in a single stack trace.
	// Since events contain only stack id rather than whole stack trace,
	// we can allow quite large values here.
	traceStackSize = 128
	// Identifier of a fake P that is used when we trace without a real P.
	traceGlobProc = -1
	// Maximum number of bytes to encode uint64 in base-128.
	traceBytesPerNumber = 10
	// Shift of the number of arguments in the first event byte.
	traceArgCountShift = 6
	// Flag passed to traceGoPark to denote that the previous wakeup of this
	// goroutine was futile. For example, a goroutine was unblocked on a mutex,
	// but another goroutine got ahead and acquired the mutex before the first
	// goroutine is scheduled, so the first goroutine has to block again.
	// Such wakeups happen on buffered channels and sync.Mutex,
	// but are generally not interesting for end user.
	traceFutileWakeup byte = 128
)

// trace is global tracing context.
var trace struct {
	lock          mutex       // protects the following members
	lockOwner     *g          // to avoid deadlocks during recursive lock locks
	enabled       bool        // when set runtime traces events
	shutdown      bool        // set when we are waiting for trace reader to finish after setting enabled to false
	headerWritten bool        // whether ReadTrace has emitted trace header
	footerWritten bool        // whether ReadTrace has emitted trace footer
	shutdownSema  uint32      // used to wait for ReadTrace completion
	seqStart      uint64      // sequence number when tracing was started
	ticksStart    int64       // cputicks when tracing was started
	ticksEnd      int64       // cputicks when tracing was stopped
	timeStart     int64       // nanotime when tracing was started
	timeEnd       int64       // nanotime when tracing was stopped
	seqGC         uint64      // GC start/done sequencer
	reading       traceBufPtr // buffer currently handed off to user
	empty         traceBufPtr // stack of empty buffers
	fullHead      traceBufPtr // queue of full buffers
	fullTail      traceBufPtr
	reader        *g              // goroutine that called ReadTrace, or nil
	stackTab      traceStackTable // maps stack traces to unique ids

	// Dictionary for traceEvString.
	// Currently this is used only for func/file:line info after tracing session,
	// so we assume single-threaded access.
	strings   map[string]uint64
	stringSeq uint64

	bufLock mutex       // protects buf
	buf     traceBufPtr // global trace buffer, used when running without a p
}

// traceBufHeader is per-P tracing buffer.
type traceBufHeader struct {
	link      traceBufPtr              // in trace.empty/full
	lastTicks uint64                   // when we wrote the last event
	pos       int                      // next write offset in arr
	stk       [traceStackSize]location // scratch buffer for traceback
}

// traceBuf is per-P tracing buffer.
type traceBuf struct {
	traceBufHeader
	arr [64<<10 - unsafe.Sizeof(traceBufHeader{})]byte // underlying buffer for traceBufHeader.buf
}

// traceBufPtr is a *traceBuf that is not traced by the garbage
// collector and doesn't have write barriers. traceBufs are not
// allocated from the GC'd heap, so this is safe, and are often
// manipulated in contexts where write barriers are not allowed, so
// this is necessary.
type traceBufPtr uintptr

func (tp traceBufPtr) ptr() *traceBuf   { return (*traceBuf)(unsafe.Pointer(tp)) }
func (tp *traceBufPtr) set(b *traceBuf) { *tp = traceBufPtr(unsafe.Pointer(b)) }
func traceBufPtrOf(b *traceBuf) traceBufPtr {
	return traceBufPtr(unsafe.Pointer(b))
}

// StartTrace enables tracing for the current process.
// While tracing, the data will be buffered and available via ReadTrace.
// StartTrace returns an error if tracing is already enabled.
// Most clients should use the runtime/trace package or the testing package's
// -test.trace flag instead of calling StartTrace directly.
func StartTrace() error {
	// Stop the world, so that we can take a consistent snapshot
	// of all goroutines at the beginning of the trace.
	stopTheWorld("start tracing")

	// We are in stop-the-world, but syscalls can finish and write to trace concurrently.
	// Exitsyscall could check trace.enabled long before and then suddenly wake up
	// and decide to write to trace at a random point in time.
	// However, such syscall will use the global trace.buf buffer, because we've
	// acquired all p's by doing stop-the-world. So this protects us from such races.
	lock(&trace.bufLock)

	if trace.enabled || trace.shutdown {
		unlock(&trace.bufLock)
		startTheWorld()
		return errorString("tracing is already enabled")
	}

	// Can't set trace.enabled yet. While the world is stopped, exitsyscall could
	// already emit a delayed event (see exitTicks in exitsyscall) if we set trace.enabled here.
	// That would lead to an inconsistent trace:
	// - either GoSysExit appears before EvGoInSyscall,
	// - or GoSysExit appears for a goroutine for which we don't emit EvGoInSyscall below.
	// To instruct traceEvent that it must not ignore events below, we set startingtrace.
	// trace.enabled is set afterwards once we have emitted all preliminary events.
	_g_ := getg()
	_g_.m.startingtrace = true
	for _, gp := range allgs {
		status := readgstatus(gp)
		if status != _Gdead {
			traceGoCreate(gp, gp.startpc) // also resets gp.traceseq/tracelastp
		}
		if status == _Gwaiting {
			// traceEvGoWaiting is implied to have seq=1.
			gp.traceseq++
			traceEvent(traceEvGoWaiting, -1, uint64(gp.goid))
		}
		if status == _Gsyscall {
			gp.traceseq++
			traceEvent(traceEvGoInSyscall, -1, uint64(gp.goid))
		} else {
			gp.sysblocktraced = false
		}
	}
	traceProcStart()
	traceGoStart()
	// Note: ticksStart needs to be set after we emit traceEvGoInSyscall events.
	// If we do it the other way around, it is possible that exitsyscall will
	// query sysexitticks after ticksStart but before traceEvGoInSyscall timestamp.
	// It will lead to a false conclusion that cputicks is broken.
	trace.ticksStart = cputicks()
	trace.timeStart = nanotime()
	trace.headerWritten = false
	trace.footerWritten = false
	trace.strings = make(map[string]uint64)
	trace.stringSeq = 0
	trace.seqGC = 0
	_g_.m.startingtrace = false
	trace.enabled = true

	unlock(&trace.bufLock)

	startTheWorld()
	return nil
}

// StopTrace stops tracing, if it was previously enabled.
// StopTrace only returns after all the reads for the trace have completed.
func StopTrace() {
	// Stop the world so that we can collect the trace buffers from all p's below,
	// and also to avoid races with traceEvent.
	stopTheWorld("stop tracing")

	// See the comment in StartTrace.
	lock(&trace.bufLock)

	if !trace.enabled {
		unlock(&trace.bufLock)
		startTheWorld()
		return
	}

	traceGoSched()

	for _, p := range &allp {
		if p == nil {
			break
		}
		buf := p.tracebuf
		if buf != 0 {
			traceFullQueue(buf)
			p.tracebuf = 0
		}
	}
	if trace.buf != 0 && trace.buf.ptr().pos != 0 {
		buf := trace.buf
		trace.buf = 0
		traceFullQueue(buf)
	}

	for {
		trace.ticksEnd = cputicks()
		trace.timeEnd = nanotime()
		// Windows time can tick only every 15ms, wait for at least one tick.
		if trace.timeEnd != trace.timeStart {
			break
		}
		osyield()
	}

	trace.enabled = false
	trace.shutdown = true
	unlock(&trace.bufLock)

	startTheWorld()

	// The world is started but we've set trace.shutdown, so new tracing can't start.
	// Wait for the trace reader to flush pending buffers and stop.
	semacquire(&trace.shutdownSema, false)
	if raceenabled {
		raceacquire(unsafe.Pointer(&trace.shutdownSema))
	}

	// The lock protects us from races with StartTrace/StopTrace because they do stop-the-world.
	lock(&trace.lock)
	for _, p := range &allp {
		if p == nil {
			break
		}
		if p.tracebuf != 0 {
			throw("trace: non-empty trace buffer in proc")
		}
	}
	if trace.buf != 0 {
		throw("trace: non-empty global trace buffer")
	}
	if trace.fullHead != 0 || trace.fullTail != 0 {
		throw("trace: non-empty full trace buffer")
	}
	if trace.reading != 0 || trace.reader != nil {
		throw("trace: reading after shutdown")
	}
	for trace.empty != 0 {
		buf := trace.empty
		trace.empty = buf.ptr().link
		sysFree(unsafe.Pointer(buf), unsafe.Sizeof(*buf.ptr()), &memstats.other_sys)
	}
	trace.strings = nil
	trace.shutdown = false
	unlock(&trace.lock)
}

// ReadTrace returns the next chunk of binary tracing data, blocking until data
// is available. If tracing is turned off and all the data accumulated while it
// was on has been returned, ReadTrace returns nil. The caller must copy the
// returned data before calling ReadTrace again.
// ReadTrace must be called from one goroutine at a time.
func ReadTrace() []byte {
	// This function may need to lock trace.lock recursively
	// (goparkunlock -> traceGoPark -> traceEvent -> traceFlush).
	// To allow this we use trace.lockOwner.
	// Also this function must not allocate while holding trace.lock:
	// allocation can call heap allocate, which will try to emit a trace
	// event while holding heap lock.
	lock(&trace.lock)
	trace.lockOwner = getg()

	if trace.reader != nil {
		// More than one goroutine reads trace. This is bad.
		// But we rather do not crash the program because of tracing,
		// because tracing can be enabled at runtime on prod servers.
		trace.lockOwner = nil
		unlock(&trace.lock)
		println("runtime: ReadTrace called from multiple goroutines simultaneously")
		return nil
	}
	// Recycle the old buffer.
	if buf := trace.reading; buf != 0 {
		buf.ptr().link = trace.empty
		trace.empty = buf
		trace.reading = 0
	}
	// Write trace header.
	if !trace.headerWritten {
		trace.headerWritten = true
		trace.lockOwner = nil
		unlock(&trace.lock)
		return []byte("go 1.7 trace\x00\x00\x00\x00")
	}
	// Wait for new data.
	if trace.fullHead == 0 && !trace.shutdown {
		trace.reader = getg()
		goparkunlock(&trace.lock, "trace reader (blocked)", traceEvGoBlock, 2)
		lock(&trace.lock)
	}
	// Write a buffer.
	if trace.fullHead != 0 {
		buf := traceFullDequeue()
		trace.reading = buf
		trace.lockOwner = nil
		unlock(&trace.lock)
		return buf.ptr().arr[:buf.ptr().pos]
	}
	// Write footer with timer frequency.
	if !trace.footerWritten {
		trace.footerWritten = true
		// Use float64 because (trace.ticksEnd - trace.ticksStart) * 1e9 can overflow int64.
		freq := float64(trace.ticksEnd-trace.ticksStart) * 1e9 / float64(trace.timeEnd-trace.timeStart) / traceTickDiv
		trace.lockOwner = nil
		unlock(&trace.lock)
		var data []byte
		data = append(data, traceEvFrequency|0<<traceArgCountShift)
		data = traceAppend(data, uint64(freq))
		if timers.gp != nil {
			data = append(data, traceEvTimerGoroutine|0<<traceArgCountShift)
			data = traceAppend(data, uint64(timers.gp.goid))
		}
		// This will emit a bunch of full buffers, we will pick them up
		// on the next iteration.
		trace.stackTab.dump()
		return data
	}
	// Done.
	if trace.shutdown {
		trace.lockOwner = nil
		unlock(&trace.lock)
		if raceenabled {
			// Model synchronization on trace.shutdownSema, which race
			// detector does not see. This is required to avoid false
			// race reports on writer passed to trace.Start.
			racerelease(unsafe.Pointer(&trace.shutdownSema))
		}
		// trace.enabled is already reset, so can call traceable functions.
		semrelease(&trace.shutdownSema)
		return nil
	}
	// Also bad, but see the comment above.
	trace.lockOwner = nil
	unlock(&trace.lock)
	println("runtime: spurious wakeup of trace reader")
	return nil
}

// traceReader returns the trace reader that should be woken up, if any.
func traceReader() *g {
	if trace.reader == nil || (trace.fullHead == 0 && !trace.shutdown) {
		return nil
	}
	lock(&trace.lock)
	if trace.reader == nil || (trace.fullHead == 0 && !trace.shutdown) {
		unlock(&trace.lock)
		return nil
	}
	gp := trace.reader
	trace.reader = nil
	unlock(&trace.lock)
	return gp
}

// traceProcFree frees trace buffer associated with pp.
func traceProcFree(pp *p) {
	buf := pp.tracebuf
	pp.tracebuf = 0
	if buf == 0 {
		return
	}
	lock(&trace.lock)
	traceFullQueue(buf)
	unlock(&trace.lock)
}

// traceFullQueue queues buf into queue of full buffers.
func traceFullQueue(buf traceBufPtr) {
	buf.ptr().link = 0
	if trace.fullHead == 0 {
		trace.fullHead = buf
	} else {
		trace.fullTail.ptr().link = buf
	}
	trace.fullTail = buf
}

// traceFullDequeue dequeues from queue of full buffers.
func traceFullDequeue() traceBufPtr {
	buf := trace.fullHead
	if buf == 0 {
		return 0
	}
	trace.fullHead = buf.ptr().link
	if trace.fullHead == 0 {
		trace.fullTail = 0
	}
	buf.ptr().link = 0
	return buf
}

// traceEvent writes a single event to trace buffer, flushing the buffer if necessary.
// ev is event type.
// If skip > 0, write current stack id as the last argument (skipping skip top frames).
// If skip = 0, this event type should contain a stack, but we don't want
// to collect and remember it for this particular call.
func traceEvent(ev byte, skip int, args ...uint64) {
	mp, pid, bufp := traceAcquireBuffer()
	// Double-check trace.enabled now that we've done m.locks++ and acquired bufLock.
	// This protects from races between traceEvent and StartTrace/StopTrace.

	// The caller checked that trace.enabled == true, but trace.enabled might have been
	// turned off between the check and now. Check again. traceLockBuffer did mp.locks++,
	// StopTrace does stopTheWorld, and stopTheWorld waits for mp.locks to go back to zero,
	// so if we see trace.enabled == true now, we know it's true for the rest of the function.
	// Exitsyscall can run even during stopTheWorld. The race with StartTrace/StopTrace
	// during tracing in exitsyscall is resolved by locking trace.bufLock in traceLockBuffer.
	if !trace.enabled && !mp.startingtrace {
		traceReleaseBuffer(pid)
		return
	}
	buf := (*bufp).ptr()
	const maxSize = 2 + 5*traceBytesPerNumber // event type, length, sequence, timestamp, stack id and two add params
	if buf == nil || len(buf.arr)-buf.pos < maxSize {
		buf = traceFlush(traceBufPtrOf(buf)).ptr()
		(*bufp).set(buf)
	}

	ticks := uint64(cputicks()) / traceTickDiv
	tickDiff := ticks - buf.lastTicks
	if buf.pos == 0 {
		buf.byte(traceEvBatch | 1<<traceArgCountShift)
		buf.varint(uint64(pid))
		buf.varint(ticks)
		tickDiff = 0
	}
	buf.lastTicks = ticks
	narg := byte(len(args))
	if skip >= 0 {
		narg++
	}
	// We have only 2 bits for number of arguments.
	// If number is >= 3, then the event type is followed by event length in bytes.
	if narg > 3 {
		narg = 3
	}
	startPos := buf.pos
	buf.byte(ev | narg<<traceArgCountShift)
	var lenp *byte
	if narg == 3 {
		// Reserve the byte for length assuming that length < 128.
		buf.varint(0)
		lenp = &buf.arr[buf.pos-1]
	}
	buf.varint(tickDiff)
	for _, a := range args {
		buf.varint(a)
	}
	if skip == 0 {
		buf.varint(0)
	} else if skip > 0 {
		_g_ := getg()
		gp := mp.curg
		var nstk int
		if gp == _g_ {
			nstk = callers(skip, buf.stk[:])
		} else if gp != nil {
			// FIXME: get stack trace of different goroutine.
		}
		if nstk > 0 {
			nstk-- // skip runtime.goexit
		}
		if nstk > 0 && gp.goid == 1 {
			nstk-- // skip runtime.main
		}
		id := trace.stackTab.put(buf.stk[:nstk])
		buf.varint(uint64(id))
	}
	evSize := buf.pos - startPos
	if evSize > maxSize {
		throw("invalid length of trace event")
	}
	if lenp != nil {
		// Fill in actual length.
		*lenp = byte(evSize - 2)
	}
	traceReleaseBuffer(pid)
}

// traceAcquireBuffer returns trace buffer to use and, if necessary, locks it.
func traceAcquireBuffer() (mp *m, pid int32, bufp *traceBufPtr) {
	mp = acquirem()
	if p := mp.p.ptr(); p != nil {
		return mp, p.id, &p.tracebuf
	}
	lock(&trace.bufLock)
	return mp, traceGlobProc, &trace.buf
}

// traceReleaseBuffer releases a buffer previously acquired with traceAcquireBuffer.
func traceReleaseBuffer(pid int32) {
	if pid == traceGlobProc {
		unlock(&trace.bufLock)
	}
	releasem(getg().m)
}

// traceFlush puts buf onto stack of full buffers and returns an empty buffer.
func traceFlush(buf traceBufPtr) traceBufPtr {
	owner := trace.lockOwner
	dolock := owner == nil || owner != getg().m.curg
	if dolock {
		lock(&trace.lock)
	}
	if buf != 0 {
		traceFullQueue(buf)
	}
	if trace.empty != 0 {
		buf = trace.empty
		trace.empty = buf.ptr().link
	} else {
		buf = traceBufPtr(sysAlloc(unsafe.Sizeof(traceBuf{}), &memstats.other_sys))
		if buf == 0 {
			throw("trace: out of memory")
		}
	}
	bufp := buf.ptr()
	bufp.link.set(nil)
	bufp.pos = 0
	bufp.lastTicks = 0
	if dolock {
		unlock(&trace.lock)
	}
	return buf
}

func traceString(buf *traceBuf, s string) (uint64, *traceBuf) {
	if s == "" {
		return 0, buf
	}
	if id, ok := trace.strings[s]; ok {
		return id, buf
	}

	trace.stringSeq++
	id := trace.stringSeq
	trace.strings[s] = id

	size := 1 + 2*traceBytesPerNumber + len(s)
	if len(buf.arr)-buf.pos < size {
		buf = traceFlush(traceBufPtrOf(buf)).ptr()
	}
	buf.byte(traceEvString)
	buf.varint(id)
	buf.varint(uint64(len(s)))
	buf.pos += copy(buf.arr[buf.pos:], s)
	return id, buf
}

// traceAppend appends v to buf in little-endian-base-128 encoding.
func traceAppend(buf []byte, v uint64) []byte {
	for ; v >= 0x80; v >>= 7 {
		buf = append(buf, 0x80|byte(v))
	}
	buf = append(buf, byte(v))
	return buf
}

// varint appends v to buf in little-endian-base-128 encoding.
func (buf *traceBuf) varint(v uint64) {
	pos := buf.pos
	for ; v >= 0x80; v >>= 7 {
		buf.arr[pos] = 0x80 | byte(v)
		pos++
	}
	buf.arr[pos] = byte(v)
	pos++
	buf.pos = pos
}

// byte appends v to buf.
func (buf *traceBuf) byte(v byte) {
	buf.arr[buf.pos] = v
	buf.pos++
}

// traceStackTable maps stack traces (arrays of PC's) to unique uint32 ids.
// It is lock-free for reading.
type traceStackTable struct {
	lock mutex
	seq  uint32
	mem  traceAlloc
	tab  [1 << 13]traceStackPtr
}

// traceStack is a single stack in traceStackTable.
type traceStack struct {
	link traceStackPtr
	hash uintptr
	id   uint32
	n    int
	stk  [0]location // real type [n]location
}

type traceStackPtr uintptr

func (tp traceStackPtr) ptr() *traceStack { return (*traceStack)(unsafe.Pointer(tp)) }

// stack returns slice of PCs.
func (ts *traceStack) stack() []location {
	return (*[traceStackSize]location)(unsafe.Pointer(&ts.stk))[:ts.n]
}

// put returns a unique id for the stack trace pcs and caches it in the table,
// if it sees the trace for the first time.
func (tab *traceStackTable) put(pcs []location) uint32 {
	if len(pcs) == 0 {
		return 0
	}
	var hash uintptr
	for _, loc := range pcs {
		hash += loc.pc
		hash += hash << 10
		hash ^= hash >> 6
	}
	// First, search the hashtable w/o the mutex.
	if id := tab.find(pcs, hash); id != 0 {
		return id
	}
	// Now, double check under the mutex.
	lock(&tab.lock)
	if id := tab.find(pcs, hash); id != 0 {
		unlock(&tab.lock)
		return id
	}
	// Create new record.
	tab.seq++
	stk := tab.newStack(len(pcs))
	stk.hash = hash
	stk.id = tab.seq
	stk.n = len(pcs)
	stkpc := stk.stack()
	for i, pc := range pcs {
		stkpc[i] = pc
	}
	part := int(hash % uintptr(len(tab.tab)))
	stk.link = tab.tab[part]
	atomicstorep(unsafe.Pointer(&tab.tab[part]), unsafe.Pointer(stk))
	unlock(&tab.lock)
	return stk.id
}

// find checks if the stack trace pcs is already present in the table.
func (tab *traceStackTable) find(pcs []location, hash uintptr) uint32 {
	part := int(hash % uintptr(len(tab.tab)))
Search:
	for stk := tab.tab[part].ptr(); stk != nil; stk = stk.link.ptr() {
		if stk.hash == hash && stk.n == len(pcs) {
			for i, stkpc := range stk.stack() {
				if stkpc != pcs[i] {
					continue Search
				}
			}
			return stk.id
		}
	}
	return 0
}

// newStack allocates a new stack of size n.
func (tab *traceStackTable) newStack(n int) *traceStack {
	return (*traceStack)(tab.mem.alloc(unsafe.Sizeof(traceStack{}) + uintptr(n)*unsafe.Sizeof(location{})))
}

// dump writes all previously cached stacks to trace buffers,
// releases all memory and resets state.
func (tab *traceStackTable) dump() {
	var tmp [(2 + 4*traceStackSize) * traceBytesPerNumber]byte
	buf := traceFlush(0).ptr()
	for _, stk := range tab.tab {
		stk := stk.ptr()
		for ; stk != nil; stk = stk.link.ptr() {
			tmpbuf := tmp[:0]
			tmpbuf = traceAppend(tmpbuf, uint64(stk.id))
			tmpbuf = traceAppend(tmpbuf, uint64(stk.n))
			for _, pc := range stk.stack() {
				var frame traceFrame
				frame, buf = traceFrameForPC(buf, pc)
				tmpbuf = traceAppend(tmpbuf, uint64(pc.pc))
				tmpbuf = traceAppend(tmpbuf, uint64(frame.funcID))
				tmpbuf = traceAppend(tmpbuf, uint64(frame.fileID))
				tmpbuf = traceAppend(tmpbuf, uint64(frame.line))
			}
			// Now copy to the buffer.
			size := 1 + traceBytesPerNumber + len(tmpbuf)
			if len(buf.arr)-buf.pos < size {
				buf = traceFlush(traceBufPtrOf(buf)).ptr()
			}
			buf.byte(traceEvStack | 3<<traceArgCountShift)
			buf.varint(uint64(len(tmpbuf)))
			buf.pos += copy(buf.arr[buf.pos:], tmpbuf)
		}
	}

	lock(&trace.lock)
	traceFullQueue(traceBufPtrOf(buf))
	unlock(&trace.lock)

	tab.mem.drop()
	*tab = traceStackTable{}
}

type traceFrame struct {
	funcID uint64
	fileID uint64
	line   uint64
}

func traceFrameForPC(buf *traceBuf, loc location) (traceFrame, *traceBuf) {
	var frame traceFrame
	fn := loc.function
	const maxLen = 1 << 10
	if len(fn) > maxLen {
		fn = fn[len(fn)-maxLen:]
	}
	frame.funcID, buf = traceString(buf, fn)
	file, line := loc.filename, loc.lineno
	frame.line = uint64(line)
	if len(file) > maxLen {
		file = file[len(file)-maxLen:]
	}
	frame.fileID, buf = traceString(buf, file)
	return frame, buf
}

// traceAlloc is a non-thread-safe region allocator.
// It holds a linked list of traceAllocBlock.
type traceAlloc struct {
	head traceAllocBlockPtr
	off  uintptr
}

// traceAllocBlock is a block in traceAlloc.
//
// traceAllocBlock is allocated from non-GC'd memory, so it must not
// contain heap pointers. Writes to pointers to traceAllocBlocks do
// not need write barriers.
type traceAllocBlock struct {
	next traceAllocBlockPtr
	data [64<<10 - sys.PtrSize]byte
}

type traceAllocBlockPtr uintptr

func (p traceAllocBlockPtr) ptr() *traceAllocBlock   { return (*traceAllocBlock)(unsafe.Pointer(p)) }
func (p *traceAllocBlockPtr) set(x *traceAllocBlock) { *p = traceAllocBlockPtr(unsafe.Pointer(x)) }

// alloc allocates n-byte block.
func (a *traceAlloc) alloc(n uintptr) unsafe.Pointer {
	n = round(n, sys.PtrSize)
	if a.head == 0 || a.off+n > uintptr(len(a.head.ptr().data)) {
		if n > uintptr(len(a.head.ptr().data)) {
			throw("trace: alloc too large")
		}
		// This is only safe because the strings returned by callers
		// are stored in a location that is not in the Go heap.
		block := (*traceAllocBlock)(sysAlloc(unsafe.Sizeof(traceAllocBlock{}), &memstats.other_sys))
		if block == nil {
			throw("trace: out of memory")
		}
		block.next.set(a.head.ptr())
		a.head.set(block)
		a.off = 0
	}
	p := &a.head.ptr().data[a.off]
	a.off += n
	return unsafe.Pointer(p)
}

// drop frees all previously allocated memory and resets the allocator.
func (a *traceAlloc) drop() {
	for a.head != 0 {
		block := a.head.ptr()
		a.head.set(block.next.ptr())
		sysFree(unsafe.Pointer(block), unsafe.Sizeof(traceAllocBlock{}), &memstats.other_sys)
	}
}

// The following functions write specific events to trace.

func traceGomaxprocs(procs int32) {
	traceEvent(traceEvGomaxprocs, 1, uint64(procs))
}

func traceProcStart() {
	traceEvent(traceEvProcStart, -1, uint64(getg().m.id))
}

func traceProcStop(pp *p) {
	// Sysmon and stopTheWorld can stop Ps blocked in syscalls,
	// to handle this we temporary employ the P.
	mp := acquirem()
	oldp := mp.p
	mp.p.set(pp)
	traceEvent(traceEvProcStop, -1)
	mp.p = oldp
	releasem(mp)
}

func traceGCStart() {
	traceEvent(traceEvGCStart, 3, trace.seqGC)
	trace.seqGC++
}

func traceGCDone() {
	traceEvent(traceEvGCDone, -1)
}

func traceGCScanStart() {
	traceEvent(traceEvGCScanStart, -1)
}

func traceGCScanDone() {
	traceEvent(traceEvGCScanDone, -1)
}

func traceGCSweepStart() {
	traceEvent(traceEvGCSweepStart, 1)
}

func traceGCSweepDone() {
	traceEvent(traceEvGCSweepDone, -1)
}

func traceGoCreate(newg *g, pc uintptr) {
	newg.traceseq = 0
	newg.tracelastp = getg().m.p
	// +PCQuantum because traceFrameForPC expects return PCs and subtracts PCQuantum.
	id := trace.stackTab.put([]location{location{pc: pc + sys.PCQuantum}})
	traceEvent(traceEvGoCreate, 2, uint64(newg.goid), uint64(id))
}

func traceGoStart() {
	_g_ := getg().m.curg
	_p_ := _g_.m.p
	_g_.traceseq++
	if _g_.tracelastp == _p_ {
		traceEvent(traceEvGoStartLocal, -1, uint64(_g_.goid))
	} else {
		_g_.tracelastp = _p_
		traceEvent(traceEvGoStart, -1, uint64(_g_.goid), _g_.traceseq)
	}
}

func traceGoEnd() {
	traceEvent(traceEvGoEnd, -1)
}

func traceGoSched() {
	_g_ := getg()
	_g_.tracelastp = _g_.m.p
	traceEvent(traceEvGoSched, 1)
}

func traceGoPreempt() {
	_g_ := getg()
	_g_.tracelastp = _g_.m.p
	traceEvent(traceEvGoPreempt, 1)
}

func traceGoPark(traceEv byte, skip int, gp *g) {
	if traceEv&traceFutileWakeup != 0 {
		traceEvent(traceEvFutileWakeup, -1)
	}
	traceEvent(traceEv & ^traceFutileWakeup, skip)
}

func traceGoUnpark(gp *g, skip int) {
	_p_ := getg().m.p
	gp.traceseq++
	if gp.tracelastp == _p_ {
		traceEvent(traceEvGoUnblockLocal, skip, uint64(gp.goid))
	} else {
		gp.tracelastp = _p_
		traceEvent(traceEvGoUnblock, skip, uint64(gp.goid), gp.traceseq)
	}
}

func traceGoSysCall() {
	traceEvent(traceEvGoSysCall, 1)
}

func traceGoSysExit(ts int64) {
	if ts != 0 && ts < trace.ticksStart {
		// There is a race between the code that initializes sysexitticks
		// (in exitsyscall, which runs without a P, and therefore is not
		// stopped with the rest of the world) and the code that initializes
		// a new trace. The recorded sysexitticks must therefore be treated
		// as "best effort". If they are valid for this trace, then great,
		// use them for greater accuracy. But if they're not valid for this
		// trace, assume that the trace was started after the actual syscall
		// exit (but before we actually managed to start the goroutine,
		// aka right now), and assign a fresh time stamp to keep the log consistent.
		ts = 0
	}
	_g_ := getg().m.curg
	_g_.traceseq++
	_g_.tracelastp = _g_.m.p
	traceEvent(traceEvGoSysExit, -1, uint64(_g_.goid), _g_.traceseq, uint64(ts)/traceTickDiv)
}

func traceGoSysBlock(pp *p) {
	// Sysmon and stopTheWorld can declare syscalls running on remote Ps as blocked,
	// to handle this we temporary employ the P.
	mp := acquirem()
	oldp := mp.p
	mp.p.set(pp)
	traceEvent(traceEvGoSysBlock, -1)
	mp.p = oldp
	releasem(mp)
}

func traceHeapAlloc() {
	traceEvent(traceEvHeapAlloc, -1, memstats.heap_live)
}

func traceNextGC() {
	traceEvent(traceEvNextGC, -1, memstats.next_gc)
}
