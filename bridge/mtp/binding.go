package mtp

/*
#cgo CFLAGS: -I../cvendor
#cgo LDFLAGS: -L/opt/homebrew/lib -lmtp
#include "libmtp.h"
#include <stdlib.h>

// Callback context for streaming file data to Go.
typedef struct {
	int id;
} callback_ctx;

// Defined in binding_callbacks.go via //export.
// MTPDataPutFunc signature: uint16_t (void* params, void* priv, uint32_t sendlen, unsigned char *data, uint32_t *putlen)
extern uint16_t goDataPutFunc(void *params, void *priv, uint32_t sendlen, unsigned char *data, uint32_t *putlen);
// MTPDataGetFunc signature: uint16_t (void* params, void* priv, uint32_t wantlen, unsigned char *data, uint32_t *gotlen)
extern uint16_t goDataGetFunc(void *params, void *priv, uint32_t wantlen, unsigned char *data, uint32_t *gotlen);

// Wrappers that call LIBMTP functions with Go-compatible callback signatures.
static int wrap_get_file_to_handler(LIBMTP_mtpdevice_t *dev, uint32_t id, int ctx_id) {
	callback_ctx ctx;
	ctx.id = ctx_id;
	return LIBMTP_Get_File_To_Handler(dev, id,
		(MTPDataPutFunc)goDataPutFunc, (void*)&ctx,
		NULL, NULL);
}

static int wrap_send_file_from_handler(LIBMTP_mtpdevice_t *dev, LIBMTP_file_t *fi, int ctx_id) {
	callback_ctx ctx;
	ctx.id = ctx_id;
	return LIBMTP_Send_File_From_Handler(dev,
		(MTPDataGetFunc)goDataGetFunc, (void*)&ctx,
		fi, NULL, NULL);
}
*/
import "C"
import (
	"fmt"
	"io"
	"log"
	"sync"
	"unsafe"
)

// Storage represents an MTP storage (internal, SD card, etc.)
type Storage struct {
	ID          uint32
	Description string
	FreeBytes   uint64
	MaxBytes    uint64
}

// FileMeta holds metadata for an MTP object (file or folder).
type FileMeta struct {
	ID        uint32
	ParentID  uint32
	StorageID uint32
	Name      string
	Size      uint64
	ModTime   int64 // Unix timestamp
	IsFolder  bool
	FileType  int
}

// Device wraps a libmtp device pointer.
type Device struct {
	dev *C.LIBMTP_mtpdevice_t
}

// initialCallbackBuf is the size of the per-session reusable buffer
// allocated when a reader/writer registers. Sized to match libmtp's
// typical PTP transfer chunk (~22 MiB observed; 4 MiB is a safe
// default that grows if a single callback wants more). The whole point
// of holding the buffer in the registry is so we allocate ONCE per
// MTP transfer instead of ONCE per callback invocation — see
// docs/DECISIONS.md "Vanquishing the per-callback VM_ALLOCATE leak".
const initialCallbackBuf = 4 * 1024 * 1024

// readerEntry / writerEntry pair an io.Reader (or io.Writer) with a
// reusable byte buffer for the cgo callback to scratch in. Without
// the buffer, every libmtp callback would `make([]byte, wantlen)`,
// generating ~one allocation per chunk of the transfer (hundreds for
// a multi-GiB file). Go's GC frees those eventually, but macOS's
// MADV_FREE leaves them attributed to the process until kernel
// reclaim — they show as VM_ALLOCATE in vmmap and push small-RAM
// Macs into swap. Reusing one buffer per session caps Go-side memory
// at one chunk, regardless of transfer size.
type readerEntry struct {
	r   io.Reader
	buf []byte
}

type writerEntry struct {
	w   io.Writer
	buf []byte
}

// callbackRegistry maps integer IDs to reader/writer entries for streaming.
var callbackRegistry struct {
	mu      sync.Mutex
	nextID  int
	writers map[int]*writerEntry
	readers map[int]*readerEntry
}

func init() {
	callbackRegistry.writers = make(map[int]*writerEntry)
	callbackRegistry.readers = make(map[int]*readerEntry)
	C.LIBMTP_Init()
}

func registerWriter(w io.Writer) int {
	callbackRegistry.mu.Lock()
	defer callbackRegistry.mu.Unlock()
	id := callbackRegistry.nextID
	callbackRegistry.nextID++
	callbackRegistry.writers[id] = &writerEntry{w: w, buf: make([]byte, initialCallbackBuf)}
	return id
}

func unregisterWriter(id int) {
	callbackRegistry.mu.Lock()
	defer callbackRegistry.mu.Unlock()
	delete(callbackRegistry.writers, id)
}

func registerReader(r io.Reader) int {
	callbackRegistry.mu.Lock()
	defer callbackRegistry.mu.Unlock()
	id := callbackRegistry.nextID
	callbackRegistry.nextID++
	callbackRegistry.readers[id] = &readerEntry{r: r, buf: make([]byte, initialCallbackBuf)}
	return id
}

func unregisterReader(id int) {
	callbackRegistry.mu.Lock()
	defer callbackRegistry.mu.Unlock()
	delete(callbackRegistry.readers, id)
}

// DetectDevice finds and opens an available MTP device. Wraps
// DetectDeviceForLocation with locationID=0, which selects the first
// detected device — preserves the historical single-device behavior.
func DetectDevice() (*Device, error) {
	return DetectDeviceForLocation(0)
}

// DetectDeviceForLocation finds and opens an MTP device. If locationID is
// non-zero, only the device matching that macOS IOKit USB Location ID is
// considered; otherwise the first detected device is opened.
//
// Uses the raw detection API for better diagnostics.
//
// Fail-fast on libusb_claim_interface error: one attempt only. Empirically
// (2026-05-17), each failed claim on a phone in a degraded USB state can
// push the phone further out of MTP mode (Pixel 0x4EE1 → 0x4EE8 observed
// over the course of repeated retries). The Swift layer also stops at
// one attempt and surfaces an unplug-and-replug notification when this
// fails — letting the user reset the phone's USB state with a physical
// action is more reliable than us repeatedly seizing+re-enumerating.
//
// The Swift-side IOKit seize preflight (USBDeviceOpenSeize +
// USBDeviceReEnumerate) is the only thing that reliably breaks the
// kernel binding; if that succeeded and we still can't claim, retrying
// won't help because the kernel has re-bound.
func DetectDeviceForLocation(locationID uint32) (*Device, error) {
	var rawDevices *C.LIBMTP_raw_device_t
	var numDevices C.int

	log.Println("Calling LIBMTP_Detect_Raw_Devices...")
	rc := C.LIBMTP_Detect_Raw_Devices(&rawDevices, &numDevices)
	switch rc {
	case C.LIBMTP_ERROR_NO_DEVICE_ATTACHED:
		return nil, fmt.Errorf("no MTP device found (is File Transfer mode selected?)")
	case C.LIBMTP_ERROR_CONNECTING:
		return nil, fmt.Errorf("error connecting to MTP device")
	case C.LIBMTP_ERROR_MEMORY_ALLOCATION:
		return nil, fmt.Errorf("memory allocation error during MTP detection")
	case C.LIBMTP_ERROR_NONE:
		// success
	default:
		return nil, fmt.Errorf("unknown error %d during MTP detection", rc)
	}

	if numDevices == 0 || rawDevices == nil {
		return nil, fmt.Errorf("no MTP devices found")
	}
	defer C.free(unsafe.Pointer(rawDevices))

	log.Printf("Found %d raw MTP device(s)", int(numDevices))

	rawSlice := unsafe.Slice(rawDevices, int(numDevices))

	// Compute the IOKit Location ID for each detected raw device and
	// log it. With multi-device support this is the only way to know
	// which physical phone we're about to claim; with single-device it's
	// useful diagnostic noise (matches what the Swift side reads out of
	// the IORegistry).
	type candidate struct {
		raw   *C.LIBMTP_raw_device_t
		locID uint32
	}
	candidates := make([]candidate, 0, len(rawSlice))
	for i := range rawSlice {
		raw := &rawSlice[i]
		busLoc := uint32(raw.bus_location)
		devnum := uint8(raw.devnum)
		locID, err := LocationIDForBusAddr(busLoc, devnum)
		if err != nil {
			log.Printf("raw[%d] VID=0x%04x PID=0x%04x bus=%d devnum=%d locationID=<lookup failed: %v>",
				i, uint16(raw.device_entry.vendor_id), uint16(raw.device_entry.product_id),
				busLoc, devnum, err)
		} else {
			log.Printf("raw[%d] VID=0x%04x PID=0x%04x bus=%d devnum=%d locationID=0x%08x",
				i, uint16(raw.device_entry.vendor_id), uint16(raw.device_entry.product_id),
				busLoc, devnum, locID)
		}
		candidates = append(candidates, candidate{raw: raw, locID: locID})
	}

	// Pick the target raw device. If locationID was requested, find the
	// match; otherwise take candidate 0 (first detected). The historical
	// LIBMTP_Open_Raw_Device_Uncached takes a pointer into the rawDevices
	// array, so we hand it the same struct we matched.
	var target *C.LIBMTP_raw_device_t
	if locationID != 0 {
		for _, c := range candidates {
			if c.locID == locationID {
				target = c.raw
				log.Printf("Selected raw device by locationID match: 0x%08x", locationID)
				break
			}
		}
		if target == nil {
			return nil, fmt.Errorf("no MTP device with locationID=0x%08x found among %d detected device(s)", locationID, len(candidates))
		}
	} else {
		target = candidates[0].raw
		if len(candidates) > 1 {
			log.Printf("WARNING: %d MTP devices detected but --device-loc-id not set; opening the first one (locationID=0x%08x). For multi-device support pass --device-loc-id=<id> to disambiguate.",
				len(candidates), candidates[0].locID)
		}
	}

	// Log the target device's full USB descriptor tree before we try to
	// open it. This is the diagnostic that tells us whether the device
	// is exposing a PTP-class interface (kernel-claimed, can't free) or
	// a vendor-class MTP interface (free for libusb to claim).
	LogUSBInterfaces(uint16(target.device_entry.vendor_id))

	// One attempt only — fail-fast (see the function comment): retrying a
	// failed claim can push the phone further out of MTP mode.
	killCompetingProcesses()

	dev := C.LIBMTP_Open_Raw_Device_Uncached(target)
	if dev != nil {
		log.Println("MTP device opened successfully")
		return &Device{dev: dev}, nil
	}
	log.Println("Open failed (USB interface locked)")

	return nil, fmt.Errorf("failed to open MTP device — kernel-bound to the USB interface, requires physical replug or IOKit interface seize (not yet implemented)")
}

// Close releases the MTP device.
func (d *Device) Close() {
	if d.dev != nil {
		C.LIBMTP_Release_Device(d.dev)
		d.dev = nil
	}
}

// FriendlyName returns the device's friendly name, falling back to
// model name, manufacturer, or "Android Device".
func (d *Device) FriendlyName() string {
	if name := d.getCString(C.LIBMTP_Get_Friendlyname(d.dev)); name != "" {
		return name
	}
	if name := d.getCString(C.LIBMTP_Get_Modelname(d.dev)); name != "" {
		return name
	}
	if name := d.getCString(C.LIBMTP_Get_Manufacturername(d.dev)); name != "" {
		return name
	}
	return "Android Device"
}

func (d *Device) getCString(cstr *C.char) string {
	if cstr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}

// GetStorages returns all storages on the device.
func (d *Device) GetStorages() ([]Storage, error) {
	rc := C.LIBMTP_Get_Storage(d.dev, C.LIBMTP_STORAGE_SORTBY_NOTSORTED)
	if rc != 0 {
		return nil, fmt.Errorf("failed to get storages")
	}

	var storages []Storage
	for s := d.dev.storage; s != nil; s = s.next {
		desc := "Internal Storage"
		if s.StorageDescription != nil {
			desc = C.GoString(s.StorageDescription)
		}
		storages = append(storages, Storage{
			ID:          uint32(s.id),
			Description: desc,
			FreeBytes:   uint64(s.FreeSpaceInBytes),
			MaxBytes:    uint64(s.MaxCapacity),
		})
	}
	return storages, nil
}

// FilesAndFoldersRoot is the parent ID that means "root of storage".
const FilesAndFoldersRoot = 0xffffffff

// GetFilesAndFolders returns all objects (files and folders) under a given parent.
// Use FilesAndFoldersRoot for the root of a storage.
//
// libmtp signals enumeration failure two ways: (1) a NULL return AND a
// non-empty error stack, or (2) a NULL return on an actually-empty directory.
// We must distinguish them — caching a failed enumeration as "empty" means
// the user sees an empty Finder window forever, even after the device
// recovers, until the bridge restarts. The fix is to inspect the error stack
// before returning: if it has entries, the listing is unreliable and we
// surface that to the caller so it can avoid marking the directory populated.
func (d *Device) GetFilesAndFolders(storageID, parentID uint32) ([]FileMeta, error) {
	log.Printf("MTP GetFilesAndFolders(storage=%d, parent=0x%x)", storageID, parentID)
	files := C.LIBMTP_Get_Files_And_Folders(d.dev, C.uint32_t(storageID), C.uint32_t(parentID))

	// Check for MTP errors. The error stack is per-device, so we must drain
	// and clear it whether the call succeeded or not.
	var hadErr bool
	var firstErrText string
	for e := C.LIBMTP_Get_Errorstack(d.dev); e != nil; e = e.next {
		txt := C.GoString(e.error_text)
		log.Printf("MTP error: %s", txt)
		if !hadErr {
			firstErrText = txt
		}
		hadErr = true
	}
	if hadErr {
		C.LIBMTP_Clear_Errorstack(d.dev)
	}

	var result []FileMeta
	for f := files; f != nil; {
		meta := FileMeta{
			ID:        uint32(f.item_id),
			ParentID:  uint32(f.parent_id),
			StorageID: uint32(f.storage_id),
			Name:      C.GoString(f.filename),
			Size:      uint64(f.filesize),
			ModTime:   int64(f.modificationdate),
			FileType:  int(f.filetype),
			IsFolder:  f.filetype == C.LIBMTP_FILETYPE_FOLDER,
		}
		result = append(result, meta)
		next := f.next
		C.LIBMTP_destroy_file_t(f)
		f = next
	}
	if hadErr {
		return result, fmt.Errorf("LIBMTP_Get_Files_And_Folders: %s", firstErrText)
	}
	return result, nil
}

// GetFileToWriter streams a file from the device to the given writer.
func (d *Device) GetFileToWriter(objectID uint32, w io.Writer) error {
	cbID := registerWriter(w)
	defer unregisterWriter(cbID)

	rc := C.wrap_get_file_to_handler(d.dev, C.uint32_t(objectID), C.int(cbID))
	if rc != 0 {
		return fmt.Errorf("LIBMTP_Get_File_To_Handler failed for object %d", objectID)
	}
	return nil
}

// SendFileFromReader uploads a file to the device from the given reader.
func (d *Device) SendFileFromReader(parentID, storageID uint32, name string, size uint64, r io.Reader) (uint32, error) {
	cbID := registerReader(r)
	defer unregisterReader(cbID)

	fi := C.LIBMTP_new_file_t()
	defer C.LIBMTP_destroy_file_t(fi)
	// LIBMTP_destroy_file_t frees fi.filename, so let it own the CString.
	fi.filename = C.CString(name)
	fi.parent_id = C.uint32_t(parentID)
	fi.storage_id = C.uint32_t(storageID)
	fi.filesize = C.uint64_t(size)
	fi.filetype = C.LIBMTP_FILETYPE_UNKNOWN

	log.Printf("MTP SendFile(parent=0x%x, storage=0x%x, name=%q, size=%d)", parentID, storageID, name, size)
	rc := C.wrap_send_file_from_handler(d.dev, fi, C.int(cbID))
	if rc != 0 {
		d.dumpErrors()
		return 0, fmt.Errorf("LIBMTP_Send_File_From_Handler failed")
	}
	return uint32(fi.item_id), nil
}

// CheckCapabilityGetPartialObject reports whether the device advertises support
// for LIBMTP_GetPartialObject (partial-range reads of objects without
// downloading the whole thing first). Critical for the prefetch redesign
// (docs/PLAN-PREFETCH-REDESIGN.md) — devices that don't support it force the
// handler-callback approach instead of the chunked-read approach.
func (d *Device) CheckCapabilityGetPartialObject() bool {
	return C.LIBMTP_Check_Capability(d.dev, C.LIBMTP_DEVICECAP_GetPartialObject) != 0
}

// GetPartialObject reads up to maxBytes from objectID starting at offset.
// Returns the bytes actually read (may be shorter than maxBytes if offset+maxBytes
// exceeds the object size, which is fine — libmtp truncates cleanly).
//
// Allocates a libmtp-managed buffer via LIBMTP_GetPartialObject and copies it
// into a Go slice before freeing the C buffer. The Go slice is independent of
// libmtp's allocator after this returns.
//
// Used by mtpfsal.VirtualRead for ranged reads off the device (one
// rsize-bounded call per NFS READ) and by the empirical probe
// (bridge/cmd/prefetch-probe/).
func (d *Device) GetPartialObject(objectID uint32, offset uint64, maxBytes uint32) ([]byte, error) {
	var data *C.uchar
	var size C.uint
	rc := C.LIBMTP_GetPartialObject(d.dev,
		C.uint32_t(objectID),
		C.uint64_t(offset),
		C.uint32_t(maxBytes),
		&data,
		&size)
	if rc != 0 {
		d.dumpErrors()
		return nil, fmt.Errorf("LIBMTP_GetPartialObject failed for object %d offset %d", objectID, offset)
	}
	if data == nil || size == 0 {
		// Some devices return success-with-zero-bytes for offset >= filesize.
		return nil, nil
	}
	// Copy into Go memory, then free libmtp's buffer. libmtp documents free()
	// as the correct deallocator for the data pointer.
	out := C.GoBytes(unsafe.Pointer(data), C.int(size))
	C.free(unsafe.Pointer(data))
	return out, nil
}

// DeleteObject deletes an object on the device.
func (d *Device) DeleteObject(objectID uint32) error {
	rc := C.LIBMTP_Delete_Object(d.dev, C.uint32_t(objectID))
	if rc != 0 {
		d.dumpErrors()
		return fmt.Errorf("LIBMTP_Delete_Object failed for object %d", objectID)
	}
	return nil
}

// SetObjectName renames an object (file or folder) in place by setting its
// filename property — no copy, no new object ID. MTP has no move-between-parents
// op, so this only changes the name within the same parent; a cross-folder move
// is still copy+delete at the caller. LIBMTP_Set_Object_Filename takes a
// non-const char*, so the name is duplicated into a C string libmtp may write to.
func (d *Device) SetObjectName(objectID uint32, name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	log.Printf("MTP SetObjectName(object=0x%x, name=%q)", objectID, name)
	rc := C.LIBMTP_Set_Object_Filename(d.dev, C.uint32_t(objectID), cname)
	if rc != 0 {
		d.dumpErrors()
		return fmt.Errorf("LIBMTP_Set_Object_Filename failed for object %d → %q", objectID, name)
	}
	return nil
}

// CreateFolder creates a folder on the device and returns the new folder's object ID.
func (d *Device) CreateFolder(name string, parentID, storageID uint32) (uint32, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	log.Printf("MTP CreateFolder(name=%q, parent=0x%x, storage=0x%x)", name, parentID, storageID)
	id := C.LIBMTP_Create_Folder(d.dev, cname, C.uint32_t(parentID), C.uint32_t(storageID))
	if id == 0 {
		d.dumpErrors()
		return 0, fmt.Errorf("LIBMTP_Create_Folder failed for %q", name)
	}
	return uint32(id), nil
}

// dumpErrors logs and clears the MTP error stack.
func (d *Device) dumpErrors() {
	errs := C.LIBMTP_Get_Errorstack(d.dev)
	for e := errs; e != nil; e = e.next {
		log.Printf("MTP error: %s", C.GoString(e.error_text))
	}
	if errs != nil {
		C.LIBMTP_Clear_Errorstack(d.dev)
	}
}

