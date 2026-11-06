# Dual ride modes

Run migration 15 before deploying the API. Deploy the admin and both Android clients against the same API, then have an administrator save the hailing percentage, choose a shared fee of TZS 500–1,000 and enable sharing. Existing hailing payment rules continue to apply to requests predating the new configuration. Disabling sharing stops new bookings while participants can finish existing trips.

The driver enters passenger capacity excluding the driver, seats offered, price per seat and a wait of 6–180 minutes. Pickup and destination are common to every passenger. The passenger app refreshes nearby pins and the list while open. A hold consumes availability for five minutes and counts as verified only after the driver accepts the passenger's six-digit pickup code. Five incorrect attempts release the seat. New holds close five minutes before the wait deadline. An expired wait cancels outstanding commitments without charging a fee.

One confirmed passenger and zero pending holds are required to start. Completion debits the driver's wallet and credits platform revenue once for the whole trip. Cash fares are paid to the driver. A hailing request snapshots the configured percentage, and the existing ride payment settlement applies it to the completed fare.

## Staging acceptance

With two devices and an approved, financially eligible bajaji or car, publish Mbande to Gerezani/Posta. Check pickup discovery, reservation expiry, code verification, reconnect, single-seat contention and completion. Inspect the driver debit and platform credit. Request a hailed ride, take the driver through each state, collect the cash fare, and compare the receipt with the percentage at request time. Use staging accounts and wallets.
