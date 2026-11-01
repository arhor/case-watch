package auth

import "testing"

func TestPolicyShouldApplySharedEligibilityWhenUserRequested(t *testing.T) {
	// Given
	ids := []int64{123}
	restricted := New(false, ids)
	all := New(true, nil)
	ids[0] = 456
	// When
	restrictedOwn, restrictedOther, allOther, invalid := restricted.Allows(123), restricted.Allows(456), all.Allows(456), all.Allows(0)
	// Then
	if !restrictedOwn || restrictedOther || !allOther || invalid || all.Allows(-1) {
		t.Fatal("incorrect shared eligibility")
	}
}

func TestAuthenticateShouldRejectIneligibleIdentityWhenTransportIdentityInvalid(t *testing.T) {
	for _, identity := range []Identity{{}, {123, 123, "group", false}, {123, 123, "supergroup", false}, {123, 123, "channel", false}, {0, 123, "private", false}, {123, 456, "private", false}, {123, 123, "private", true}, {-1, -1, "private", false}} {
		t.Run(identity.ChatType, func(t *testing.T) {
			// Given
			policy := New(true, nil)
			// When
			owner, err := policy.Authenticate(identity)
			// Then
			if err == nil || owner.ID() != 0 {
				t.Fatal("invalid identity authenticated")
			}
		})
	}
}

func TestAuthenticateShouldBindSenderAndIsolateOwnershipWhenAllowAllEnabled(t *testing.T) {
	// Given
	policy := New(true, nil)
	// When
	owner, err := policy.Authenticate(Identity{123, 123, "private", false})
	// Then
	if err != nil || owner.ID() != 123 || owner.RequireOwner(123) != nil || owner.RequireOwner(456) == nil || (Owner{}).RequireOwner(0) == nil {
		t.Fatal("ownership was not isolated")
	}
}

func TestAuthenticateShouldRejectUnlistedSenderWhenRestricted(t *testing.T) {
	// Given
	policy := New(false, []int64{123})
	// When
	_, err := policy.Authenticate(Identity{456, 456, "private", false})
	// Then
	if err == nil {
		t.Fatal("unlisted sender authenticated")
	}
}
