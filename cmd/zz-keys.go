/*
Copyright © 2020 Alessandro Segala (@ItalyPaleAle)

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/

package cmd

import (
	"bytes"
	"errors"
	"strconv"

	"github.com/ItalyPaleAle/prvt/crypto"
	"github.com/ItalyPaleAle/prvt/infofile"
	"github.com/ItalyPaleAle/prvt/utils"

	"github.com/manifoldco/promptui"
)

// PromptPassphrase prompts the user for a passphrase
func PromptPassphrase() (string, error) {
	prompt := promptui.Prompt{
		Validate: func(input string) error {
			if len(input) < 1 {
				return errors.New("Passphrase must not be empty")
			}
			return nil
		},
		Label: "Passphrase",
		Mask:  '*',
	}

	key, err := prompt.Run()
	if err != nil {
		return "", err
	}

	return key, err
}

// NewInfoFile generates a new info file with a brand-new master key, wrapped either with a passphrase-derived key, or with GPG
func NewInfoFile(gpgKey string) (info *infofile.InfoFile, errMessage string, err error) {
	// First, create the info file
	info, err = infofile.New()
	if err != nil {
		return nil, "Error creating info file", err
	}

	// Generate the master key
	masterKey, err := crypto.NewKey()
	if err != nil {
		return nil, "Error generating the master key", err
	}

	// Add the key
	errMessage, err = AddKey(info, masterKey, gpgKey)
	if err != nil {
		info = nil
	}

	return info, "", nil
}

// UpgradeInfoFile upgrades an info file to the latest version
func UpgradeInfoFile(info *infofile.InfoFile) (errMessage string, err error) {
	// Can only upgrade info files version 1 and 2
	if info.Version != 1 && info.Version != 2 {
		return "Unsupported repository version", errors.New("This repository has already been upgraded or is using an unsupported version")
	}

	// Upgrade 1 -> 2
	if info.Version < 2 {
		errMessage, err = upgradeInfoFileV1(info)
		if err != nil {
			return errMessage, err
		}
	}

	// Upgrade 2 -> 3
	// Nothing to do here, as the change is just in the index file
	// However, we still want to update the info file so older versions of prvt won't try to open a protobuf-encoded index file
	/*if info.Version < 3 {
	}*/

	// Update the version
	info.Version = 3

	return "", nil
}

// Upgrade an info file from version 1 to 2
func upgradeInfoFileV1(info *infofile.InfoFile) (errMessage string, err error) {
	// GPG keys are already migrated into the Keys slice
	// But passphrases need to be migrated
	if len(info.Salt) > 0 && len(info.ConfirmationHash) > 0 {
		// Prompt for the passphrase to get the current master key
		passphrase, err := PromptPassphrase()
		if err != nil {
			return "Error getting passphrase", err
		}

		// Get the current master key from the passphrase
		masterKey, confirmationHash, err := crypto.KeyFromPassphrase(passphrase, info.Salt)
		if err != nil || bytes.Compare(info.ConfirmationHash, confirmationHash) != 0 {
			return "Cannot unlock the repository", errors.New("Invalid passphrase")
		}

		// Create a new salt
		newSalt, err := crypto.NewSalt()
		if err != nil {
			return "Error generating a new salt", err
		}

		// Create a new wrapping key
		wrappingKey, newConfirmationHash, err := crypto.KeyFromPassphrase(passphrase, newSalt)
		if err != nil {
			return "Error deriving the wrapping key", err
		}

		// Wrap the key
		wrappedKey, err := crypto.WrapKey(wrappingKey, masterKey)
		if err != nil {
			return "Error wrapping the master key", err
		}

		// Add the key
		err = info.AddPassphrase(newSalt, newConfirmationHash, wrappedKey)
		if err != nil {
			return "Error adding the key", err
		}

		// Remove the old key
		info.Salt = nil
		info.ConfirmationHash = nil
	}

	return "", nil
}

// AddKey adds a key to an info file
// If the GPG Key is empty, will prompt for a passphrase
func AddKey(info *infofile.InfoFile, masterKey []byte, gpgKey string) (errMessage string, err error) {
	var salt, confirmationHash, wrappedKey []byte

	// No GPG key specified, so we need to prompt for a passphrase first
	if gpgKey == "" {
		// Get the passphrase and derive the wrapping key, after generating a new salt
		passphrase, err := PromptPassphrase()
		if err != nil {
			return "Error getting passphrase", err
		}
		salt, err = crypto.NewSalt()
		if err != nil {
			return "Error generating a new salt", err
		}
		var wrappingKey []byte
		wrappingKey, confirmationHash, err = crypto.KeyFromPassphrase(passphrase, salt)
		if err != nil {
			return "Error deriving the wrapping key", err
		}

		// Wrap the key
		wrappedKey, err = crypto.WrapKey(wrappingKey, masterKey)
		if err != nil {
			return "Error wrapping the master key", err
		}

		// Add the key
		err = info.AddPassphrase(salt, confirmationHash, wrappedKey)
		if err != nil {
			return "Error adding the key", err
		}
	} else {
		// Use GPG to wrap the master key
		wrappedKey, err = utils.GPGEncrypt(masterKey, gpgKey)
		if err != nil {
			return "Error encrypting the master key with GPG", err
		}

		// Add the key
		err = info.AddGPGWrappedKey(gpgKey, wrappedKey)
		if err != nil {
			return "Error adding the key", err
		}
	}

	return "", nil
}

// GetMasterKey gets the master key, either deriving it from a passphrase, or from GPG
func GetMasterKey(info *infofile.InfoFile) (masterKey []byte, keyId string, errMessage string, err error) {
	// Iterate through all the keys
	// First, try all keys that are wrapped with GPG
	for _, k := range info.Keys {
		if k.GPGKey == "" || len(k.MasterKey) == 0 {
			continue
		}
		// Try decrypting with GPG
		masterKey, err = utils.GPGDecrypt(k.MasterKey)
		if err == nil {
			return masterKey, k.GPGKey, "", nil
		}
	}

	// No GPG key specified or unlocking with a GPG key was not successful
	// We'll try with passphrases; first, prompt for it
	passphrase, err := PromptPassphrase()
	if err != nil {
		return nil, "", "Error getting passphrase", err
	}

	// Check if we have a version 1 key, where the master key is directly derived from the passphrase
	if len(info.Salt) != 0 && len(info.ConfirmationHash) != 0 {
		var confirmationHash []byte
		masterKey, confirmationHash, err = crypto.KeyFromPassphrase(passphrase, info.Salt)
		if err == nil && bytes.Compare(info.ConfirmationHash, confirmationHash) == 0 {
			return masterKey, "LegacyKey", "", nil
		}
	}

	// Try all version 2 keys that are wrapped with a key derived from the passphrase
	i := 0
	for _, k := range info.Keys {
		if k.GPGKey != "" || len(k.MasterKey) == 0 {
			continue
		}

		// Ensure we have the salt and confirmation hash
		if len(k.Salt) == 0 || len(k.ConfirmationHash) == 0 {
			continue
		}

		// Try this key
		var wrappingKey, confirmationHash []byte
		wrappingKey, confirmationHash, err = crypto.KeyFromPassphrase(passphrase, k.Salt)
		if err == nil && bytes.Compare(k.ConfirmationHash, confirmationHash) == 0 {
			masterKey, err = crypto.UnwrapKey(wrappingKey, k.MasterKey)
			if err != nil {
				return nil, "", "Error while unwrapping the master key", err
			}
			return masterKey, "p:" + strconv.Itoa(i), "", nil
		}

		i++
	}

	// Tried all keys and nothing worked
	return nil, "", "Cannot unlock the repository", errors.New("Invalid passphrase")
}
