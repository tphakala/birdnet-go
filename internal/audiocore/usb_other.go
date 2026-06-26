//go:build !linux

package audiocore

// resolveUSBIdentity is a no-op on non-Linux platforms. Stable USB identity is a
// Linux ALSA concern (the ALSA index instability described in GH #3651 does not
// apply to the WASAPI/CoreAudio backends), so this returns a zero usbIdentity and
// device matching uses the legacy id/name tiers.
func resolveUSBIdentity(decodedID, root string) usbIdentity {
	_ = decodedID
	_ = root
	return usbIdentity{}
}

// readProcAsoundCards has no meaning off Linux; it returns nil so batch callers
// (listDevices) resolve every device to a zero usbIdentity.
func readProcAsoundCards(root string) map[int]procCardEntry {
	_ = root
	return nil
}

// usbIdentityForCard is a no-op on non-Linux platforms.
func usbIdentityForCard(card int, cards map[int]procCardEntry, root string) usbIdentity {
	_ = card
	_ = cards
	_ = root
	return usbIdentity{}
}
