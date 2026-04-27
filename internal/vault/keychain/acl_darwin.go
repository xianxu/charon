//go:build darwin && cgo

package keychain

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// charon_set_generic_password upserts a generic-password keychain item.
//
// If with_acl is non-zero, NEW entries (entries the SecItemUpdate path
// didn't find) are created with an SecAccess that trusts only the
// current process's designated requirement; the OS prompts Allow/Deny
// for any other reader. EXISTING entries are updated in place via
// SecItemUpdate, which preserves their existing ACL — so token rotation
// doesn't briefly drop the ACL between delete-and-add.
//
// If with_acl is 0, the entry is written with no SecAccess, and macOS
// applies its default for SecItemAdd-without-access (the writing
// process is trusted; everything else prompts). For our usage that's
// equivalent for ServiceDev (dev binaries are always the writer of
// their own state).
//
// Returns 0 (errSecSuccess) on success, non-zero OSStatus on failure.
//
// Memory: the function owns and releases all CF objects it creates;
// the caller-passed C strings remain owned by Go (caller frees).
static OSStatus charon_set_generic_password(
    const char *service,
    const char *account,
    const void *data,
    long data_len,
    int with_acl
) {
    OSStatus rc = errSecSuccess;
    CFStringRef cfService = NULL;
    CFStringRef cfAccount = NULL;
    CFDataRef   cfData    = NULL;

    cfService = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
    cfAccount = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
    cfData    = CFDataCreate(NULL, (const UInt8 *)data, (CFIndex)data_len);
    if (cfService == NULL || cfAccount == NULL || cfData == NULL) {
        rc = errSecAllocate;
        goto cleanup_inputs;
    }

    // Step 1: try SecItemUpdate (atomic in-place update; preserves ACL).
    {
        const void *qkeys[] = { kSecClass,                kSecAttrService, kSecAttrAccount };
        const void *qvals[] = { kSecClassGenericPassword, cfService,       cfAccount       };
        CFDictionaryRef query = CFDictionaryCreate(
            NULL, qkeys, qvals, 3,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);

        const void *ukeys[] = { kSecValueData };
        const void *uvals[] = { cfData        };
        CFDictionaryRef update = CFDictionaryCreate(
            NULL, ukeys, uvals, 1,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);

        rc = SecItemUpdate(query, update);
        CFRelease(query);
        CFRelease(update);

        if (rc == errSecSuccess) goto cleanup_inputs;
        // Fall through to add-with-fresh-ACL ONLY for "doesn't exist".
        // Other errors (notably errSecAuthFailed when an existing entry
        // has an ACL pinned to a different designated requirement than
        // the current process) propagate to the caller intentionally —
        // we don't silently re-create or overwrite an entry someone
        // else owns. Operator workaround:
        //   security delete-generic-password -s <service> -a <account>
        if (rc != errSecItemNotFound) goto cleanup_inputs;
        rc = errSecSuccess;
    }

    // Step 2: item didn't exist — SecItemAdd with a fresh ACL.
    {
        SecAccessRef access = NULL;
        if (with_acl) {
            // SecTrustedApplicationCreateFromPath(NULL, ...) represents the
            // current process; the SecAccess stores its designated requirement
            // (not its path), so the ACL evaluates by-DR for future reads
            // including reinstalls of the same DR-matching binary.
            //
            // SecTrustedApplicationCreateFromPath / SecAccessCreate are formally
            // deprecated since macOS 10.10 but remain functional for legacy
            // file-based keychains (login.keychain-db) which is what we target.
            // Modern replacements (SecAccessControlCreateWithFlags) are for
            // iOS-style biometric-gated access, not the codesign-DR ACL we want.
            SecTrustedApplicationRef self = NULL;
            rc = SecTrustedApplicationCreateFromPath(NULL, &self);
            if (rc != errSecSuccess) goto cleanup_inputs;

            CFArrayRef trustList = CFArrayCreate(
                NULL, (const void **)&self, 1, &kCFTypeArrayCallBacks);
            rc = SecAccessCreate(CFSTR("charon"), trustList, &access);
            CFRelease(self);
            CFRelease(trustList);
            if (rc != errSecSuccess) goto cleanup_inputs;
        }

        // Build attribute dictionary: include kSecAttrAccess only when we
        // built one. kSecAttrSynchronizable=false keeps entries off iCloud.
        // Note: this attribute set is only used on the SecItemAdd path —
        // the SecItemUpdate path above only updates kSecValueData, leaving
        // kSecAttrSynchronizable (and the ACL) intact on the existing
        // entry. That's intentional: we own the namespace, attributes
        // are written once at create time.
        CFTypeRef akeys[6];
        CFTypeRef avals[6];
        int n = 0;
        akeys[n] = kSecClass;                avals[n] = kSecClassGenericPassword;     n++;
        akeys[n] = kSecAttrService;          avals[n] = (CFTypeRef)cfService;         n++;
        akeys[n] = kSecAttrAccount;          avals[n] = (CFTypeRef)cfAccount;         n++;
        akeys[n] = kSecValueData;            avals[n] = (CFTypeRef)cfData;            n++;
        akeys[n] = kSecAttrSynchronizable;   avals[n] = (CFTypeRef)kCFBooleanFalse;   n++;
        if (access != NULL) {
            akeys[n] = kSecAttrAccess;       avals[n] = (CFTypeRef)access;            n++;
        }

        CFDictionaryRef addDict = CFDictionaryCreate(
            NULL,
            (const void **)akeys, (const void **)avals, n,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);

        rc = SecItemAdd(addDict, NULL);
        CFRelease(addDict);
        if (access) CFRelease(access);
    }

cleanup_inputs:
    if (cfService) CFRelease(cfService);
    if (cfAccount) CFRelease(cfAccount);
    if (cfData)    CFRelease(cfData);
    return rc;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// setGenericPassword upserts a generic-password keychain item under
// `service` + `account`, atomic via SecItemUpdate when the entry
// already exists. New entries written with `withACL=true` get an ACL
// bound to the current process's designated requirement; readers with
// a different signature trigger the macOS "Allow/Deny" dialog.
//
// Used by Store.Set and SetRaw on darwin+cgo. Both paths route through
// this; ACL is gated by the caller (typically: ACL for ServiceProd,
// no ACL for ServiceDev).
func setGenericPassword(service, account string, data []byte, withACL bool) error {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))

	var dataPtr unsafe.Pointer
	var dataLen C.long
	if len(data) > 0 {
		dataPtr = unsafe.Pointer(&data[0])
		dataLen = C.long(len(data))
	}

	withAclC := C.int(0)
	if withACL {
		withAclC = 1
	}

	rc := C.charon_set_generic_password(cService, cAccount, dataPtr, dataLen, withAclC)
	if rc != 0 {
		return fmt.Errorf("keychain Set %s/%s: OSStatus %d", service, account, int(rc))
	}
	return nil
}
