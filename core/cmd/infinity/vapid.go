package main

import (
	"fmt"

	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/spf13/cobra"
)

// vapidCmd generates a fresh VAPID keypair for Web Push and prints it
// as shell-ready exports the boss can paste into Railway's variables
// dashboard or a local .env file.
//
//	$ infinity vapid
//	VAPID_PUBLIC_KEY=BPa5z...
//	VAPID_PRIVATE_KEY=ZyXw...
//	VAPID_SUBJECT=mailto:you@example.com   # update this
//
// VAPID keys identify Core as the legitimate origin of push messages to
// Apple/Google push services. They're a one-time generation per
// production environment - don't rotate them lightly because every
// subscribed device is tied to the public key on file when it
// subscribed. Rotation invalidates all existing subscriptions.
func vapidCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vapid",
		Short: "Generate a Web Push VAPID keypair",
		Long: `Generate a Web Push VAPID keypair and print it as shell exports.

Pipe into your .env file:

  infinity vapid >> .env

Or feed straight to Railway:

  infinity vapid | while read line; do
    railway variables --service core --set "$line"
  done

Update VAPID_SUBJECT to a mailto: or https:// URL identifying you.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			priv, pub, err := push.GenerateVAPIDKeys()
			if err != nil {
				return fmt.Errorf("generate VAPID keys: %w", err)
			}
			fmt.Printf("VAPID_PUBLIC_KEY=%s\n", pub)
			fmt.Printf("VAPID_PRIVATE_KEY=%s\n", priv)
			fmt.Println("VAPID_SUBJECT=mailto:you@example.com   # update this")
			fmt.Println()
			fmt.Println("# Also set NEXT_PUBLIC_VAPID_PUBLIC_KEY on Studio so the")
			fmt.Println("# browser can subscribe without a runtime round-trip:")
			fmt.Printf("# NEXT_PUBLIC_VAPID_PUBLIC_KEY=%s\n", pub)
			return nil
		},
	}
}
