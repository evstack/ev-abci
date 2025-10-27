# Network Module TODOs & Limitations

This document lists the current limitations and future work for the `network` module. It is based on the original beta documentation and `TODO` comments found in the source code.

## High Priority

-   **Vote Signature Verification**: Currently, `MsgAttest` submissions are stored, but the vote signatures within them are not verified against the provided payload. This is a critical security feature that needs to be implemented.
-   **Multi-Attester Support**: The module is currently designed for a single-attester flow. The bitmap and quorum logic will not function correctly with multiple attesters. The design needs to be extended to properly support a multi-attester set.
-   **Validator Set Updates**: The logic for updating the validator set (when attesters join or leave) should be refined. Updates should likely only take effect at the end of an epoch to ensure consistency within a voting period.

## Medium Priority

-   **Validator Ejection Logic**: The mechanism for ejecting non-participating or malicious attesters is currently a placeholder. A robust implementation is needed to maintain the health of the attester set.
-   **State Pruning**: The module stores attestation bitmaps and signatures for each block. Logic needs to be implemented to safely prune this historical data for heights that are no longer relevant, preventing unbounded state growth.
-   **Historic Queries**: Queries for attestation data need to be improved to handle historical validator sets. The attester set can change over time, and queries should be able to reference the correct set for a given block height.
-   **Attestation Window**: There should be a limit to how far in the past a validator can submit an attestation for a block. This prevents wasted resources on attesting to very old, irrelevant blocks.

## Low Priority / Refinements

-   **Error Handling**: Review error handling in the `EndBlocker`, particularly around bitmap processing, to determine if failures should be fatal or handled more gracefully.
-   **Key Encoding**: The keys used in the store could potentially be optimized (e.g., using numerical IDs instead of string-based keys) to save space.
-   **Query Use-Cases**: Clarify the use-case for querying the full attester set, as it may change with every epoch.
