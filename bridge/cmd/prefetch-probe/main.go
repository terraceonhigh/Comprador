// prefetch-probe — empirical measurement of LIBMTP_GetPartialObject viability.
//
// Step 1 of docs/PLAN-PREFETCH-REDESIGN.md. The async prefetch redesign needs
// to know: does LIBMTP_GetPartialObject work usably on the Xperia + Pixel,
// and what's the per-chunk overhead? Single full-object reads via
// Get_File_To_Handler are known-good but monopolize the libmtp session for
// the full transfer duration (~6 min for 9 GB). If GetPartialObject is
// viable, the prefetch can chunk-and-yield; if not, we fall back to
// handler-callback yields.
//
// Usage:
//   bin/prefetch-probe                       # auto-pick first device, first file > 100 MB
//   bin/prefetch-probe -chunk=4              # 4 MB chunks (default)
//   bin/prefetch-probe -chunk=16             # 16 MB chunks
//   bin/prefetch-probe -bytes=128            # only read first 128 MB (default: 64)
//   bin/prefetch-probe -object=12345 -size=...  # specific object + expected size
//
// What it reports:
//   - Whether the device advertises GetPartialObject capability
//   - Per-chunk wall time (mean, median, max) for the chosen chunk size
//   - Aggregate throughput (MB/sec) vs the Get_File_To_Handler control
//   - Recommended chunk size based on overhead measurement
//
// This is a standalone diagnostic. It does NOT touch the bridge's session
// goroutine, NFS handler, or any production code path. It opens its own MTP
// session, runs measurements, and exits. Side-effect on the device: a few
// MB of bytes get pulled across USB and discarded.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"

	"comprador/bridge/mtp"
)

func main() {
	chunkMB := flag.Int("chunk", 4, "chunk size in MB for partial-object reads")
	totalMB := flag.Int("bytes", 64, "total bytes (MB) to read for the chunked measurement")
	objectID := flag.Uint("object", 0, "specific object ID to probe (default: auto-select first file > 100 MB)")
	objectSize := flag.Uint64("size", 0, "expected object size in bytes (only needed if -object is set)")
	skipControl := flag.Bool("skip-control", false, "skip the Get_File_To_Handler control read (the slow part)")
	flag.Parse()

	// 1. Open device — first one libmtp finds. The probe doesn't care which
	// device of multiple, since the question is "does the API work on this
	// hardware class."
	log.Printf("Detecting MTP device...")
	dev, err := mtp.DetectDevice()
	if err != nil {
		log.Fatalf("DetectDevice: %v", err)
	}
	defer dev.Close()
	log.Printf("Device: %q", dev.FriendlyName())

	// 2. Capability check — first thing the redesign cares about.
	supports := dev.CheckCapabilityGetPartialObject()
	fmt.Printf("\n=== Capability ===\n")
	fmt.Printf("LIBMTP_DEVICECAP_GetPartialObject: %v\n", supports)
	if !supports {
		fmt.Printf("\nThis device does NOT advertise GetPartialObject support.\n")
		fmt.Printf("The chunked-prefetch design (Option D in PLAN-PREFETCH-REDESIGN.md)\n")
		fmt.Printf("would fall back to the handler-callback approach on this hardware.\n")
		// Don't exit — try the call anyway. Capability flags are advisory; some
		// devices support the op but don't set the cap bit.
		fmt.Printf("Attempting the call anyway to see if it works in practice...\n\n")
	}

	// 3. Pick an object to probe.
	var probeID uint32
	var probeSize uint64
	if *objectID != 0 {
		probeID = uint32(*objectID)
		probeSize = *objectSize
		if probeSize == 0 {
			log.Fatalf("-object requires -size to know how far to read")
		}
	} else {
		probeID, probeSize = pickLargeFile(dev, 100*1024*1024) // 100 MB threshold
		if probeID == 0 {
			log.Fatalf("No file > 100 MB found on device. Pass -object=<id> -size=<bytes> for a specific target.")
		}
	}
	fmt.Printf("=== Target ===\n")
	fmt.Printf("Object ID: %d\n", probeID)
	fmt.Printf("Size: %d bytes (%.1f MB)\n", probeSize, float64(probeSize)/1024/1024)
	fmt.Printf("\n")

	// 4. Chunked-read measurement.
	chunkBytes := uint32(*chunkMB) * 1024 * 1024
	totalBytes := uint64(*totalMB) * 1024 * 1024
	if totalBytes > probeSize {
		totalBytes = probeSize
	}
	numChunks := int((totalBytes + uint64(chunkBytes) - 1) / uint64(chunkBytes))
	fmt.Printf("=== Chunked read: %d chunks of %d MB ===\n", numChunks, *chunkMB)

	durations := make([]time.Duration, 0, numChunks)
	totalRead := uint64(0)
	chunkedStart := time.Now()
	for i := 0; i < numChunks; i++ {
		offset := uint64(i) * uint64(chunkBytes)
		want := chunkBytes
		if uint64(want)+offset > totalBytes {
			want = uint32(totalBytes - offset)
		}
		t0 := time.Now()
		buf, err := dev.GetPartialObject(probeID, offset, want)
		dt := time.Since(t0)
		if err != nil {
			fmt.Printf("Chunk %d (offset=%d, want=%d): ERROR %v\n", i, offset, want, err)
			fmt.Printf("\nGetPartialObject failed. Either the device doesn't support it,\n")
			fmt.Printf("or the call has a different precondition (object type, MTP version).\n")
			os.Exit(1)
		}
		durations = append(durations, dt)
		totalRead += uint64(len(buf))
		fmt.Printf("Chunk %d (offset=%d, got=%d bytes): %v (%.1f MB/sec)\n",
			i, offset, len(buf), dt,
			float64(len(buf))/(1024*1024)/dt.Seconds())
	}
	chunkedTotal := time.Since(chunkedStart)
	fmt.Printf("\n")

	// 5. Statistics.
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	mean := sum / time.Duration(len(durations))
	median := durations[len(durations)/2]
	max := durations[len(durations)-1]
	fmt.Printf("=== Chunked stats ===\n")
	fmt.Printf("Chunks:      %d\n", len(durations))
	fmt.Printf("Per-chunk:   mean=%v median=%v max=%v\n", mean, median, max)
	fmt.Printf("Total time:  %v\n", chunkedTotal)
	fmt.Printf("Bytes read:  %d (%.1f MB)\n", totalRead, float64(totalRead)/1024/1024)
	fmt.Printf("Throughput:  %.1f MB/sec\n", float64(totalRead)/(1024*1024)/chunkedTotal.Seconds())
	fmt.Printf("\n")

	// 6. Control: same byte range via Get_File_To_Handler. This is the upper-
	// bound throughput — what we'd lose by switching to chunked.
	if !*skipControl && totalBytes <= 256*1024*1024 {
		fmt.Printf("=== Control: Get_File_To_Handler (full object, %.0f MB) ===\n",
			float64(probeSize)/1024/1024)
		fmt.Printf("(Reads the WHOLE file. May take minutes. Pass -skip-control to skip.)\n")
		controlStart := time.Now()
		controlBytes := uint64(0)
		err := dev.GetFileToWriter(probeID, countingDiscarder{n: &controlBytes})
		controlTotal := time.Since(controlStart)
		if err != nil {
			fmt.Printf("Control read failed: %v\n", err)
		} else {
			fmt.Printf("Total time:  %v\n", controlTotal)
			fmt.Printf("Bytes:       %d (%.1f MB)\n", controlBytes, float64(controlBytes)/1024/1024)
			fmt.Printf("Throughput:  %.1f MB/sec\n", float64(controlBytes)/(1024*1024)/controlTotal.Seconds())
		}
		fmt.Printf("\n")
	} else if *skipControl {
		fmt.Printf("(Control read skipped via -skip-control)\n\n")
	} else {
		fmt.Printf("(Control read skipped because -bytes (%d MB) is > 256 MB; full read would take too long)\n\n", *totalMB)
	}

	// 7. Verdict.
	fmt.Printf("=== Verdict ===\n")
	if mean < 100*time.Millisecond && median < 100*time.Millisecond {
		fmt.Printf("GetPartialObject is fast. Chunked-yield prefetch design (Option D) is viable.\n")
	} else if mean < 500*time.Millisecond {
		fmt.Printf("GetPartialObject works but has noticeable per-call overhead.\n")
		fmt.Printf("Consider 16 MB chunks instead of 4 MB to amortize.\n")
	} else {
		fmt.Printf("GetPartialObject per-call overhead is high (mean %v).\n", mean)
		fmt.Printf("Chunked design may be impractical. Fall back to handler-callback yields.\n")
	}
}

// pickLargeFile walks the device's first storage looking for a file at least
// minBytes large. Returns its object ID and actual size, or (0, 0) if none found.
// Only descends one level (root + immediate children); the probe doesn't need
// to explore the whole tree.
func pickLargeFile(dev *mtp.Device, minBytes uint64) (uint32, uint64) {
	storages, err := dev.GetStorages()
	if err != nil {
		log.Fatalf("GetStorages: %v", err)
	}
	if len(storages) == 0 {
		log.Fatalf("Device has no storages")
	}
	for _, st := range storages {
		entries, err := dev.GetFilesAndFolders(st.ID, 0) // root
		if err != nil {
			log.Printf("GetFilesAndFolders(storage=%d): %v", st.ID, err)
			continue
		}
		for _, e := range entries {
			if !e.IsFolder && e.Size >= minBytes {
				log.Printf("Found candidate at storage root: %q (%d bytes)", e.Name, e.Size)
				return e.ID, e.Size
			}
		}
		// Walk one level into subdirectories. Common case: Download/ holds the
		// large file, not the storage root.
		for _, e := range entries {
			if !e.IsFolder {
				continue
			}
			sub, err := dev.GetFilesAndFolders(st.ID, e.ID)
			if err != nil {
				continue
			}
			for _, f := range sub {
				if !f.IsFolder && f.Size >= minBytes {
					log.Printf("Found candidate in %q/: %q (%d bytes)", e.Name, f.Name, f.Size)
					return f.ID, f.Size
				}
			}
		}
	}
	return 0, 0
}

// countingDiscarder is an io.Writer that discards bytes but counts them. Used
// for the control read so we don't waste disk on a 9 GB temp file.
type countingDiscarder struct {
	n *uint64
}

func (c countingDiscarder) Write(p []byte) (int, error) {
	*c.n += uint64(len(p))
	return len(p), nil
}

// io.Writer compile-time check
var _ io.Writer = countingDiscarder{}
