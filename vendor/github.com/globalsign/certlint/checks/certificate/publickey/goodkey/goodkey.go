// Package goodkey copied from "github.com/letsencrypt/boulder/goodkey"
//
// This package is vovered under the Mozilla Public License Version 2.0
//
// Removed depency on letsencrypt core and allow key size above 4096
package goodkey

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"
	"math/big"
	"reflect"
	"sync"
)

// To generate, run: primes 2 752 | tr '\n' ,
var smallPrimeInts = []int64{
	2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47,
	53, 59, 61, 67, 71, 73, 79, 83, 89, 97, 101, 103, 107,
	109, 113, 127, 131, 137, 139, 149, 151, 157, 163, 167,
	173, 179, 181, 191, 193, 197, 199, 211, 223, 227, 229,
	233, 239, 241, 251, 257, 263, 269, 271, 277, 281, 283,
	293, 307, 311, 313, 317, 331, 337, 347, 349, 353, 359,
	367, 373, 379, 383, 389, 397, 401, 409, 419, 421, 431,
	433, 439, 443, 449, 457, 461, 463, 467, 479, 487, 491,
	499, 503, 509, 521, 523, 541, 547, 557, 563, 569, 571,
	577, 587, 593, 599, 601, 607, 613, 617, 619, 631, 641,
	643, 647, 653, 659, 661, 673, 677, 683, 691, 701, 709,
	719, 727, 733, 739, 743, 751,
}

// singleton defines the object of a Singleton pattern
var (
	smallPrimesSingleton sync.Once
	smallPrimes          []*big.Int
)

// KeyPolicy determines which types of key may be used with various boulder
// operations.
type KeyPolicy struct {
	AllowRSA           bool // Whether RSA keys should be allowed.
	AllowECDSANISTP256 bool // Whether ECDSA NISTP256 keys should be allowed.
	AllowECDSANISTP384 bool // Whether ECDSA NISTP384 keys should be allowed.
}

// NewKeyPolicy returns a KeyPolicy that allows RSA, ECDSA256 and ECDSA384.
func NewKeyPolicy() KeyPolicy {
	return KeyPolicy{
		AllowRSA:           true,
		AllowECDSANISTP256: true,
		AllowECDSANISTP384: true,
	}
}

// GoodKey returns true if the key is acceptable for both TLS use and account
// key use (our requirements are the same for either one), according to basic
// strength and algorithm checking.
// TODO: Support JsonWebKeys once go-jose migration is done.
func (policy *KeyPolicy) GoodKey(key crypto.PublicKey) error {
	switch t := key.(type) {
	case rsa.PublicKey:
		return policy.goodKeyRSA(t)
	case *rsa.PublicKey:
		return policy.goodKeyRSA(*t)
	case ecdsa.PublicKey:
		return policy.goodKeyECDSA(t)
	case *ecdsa.PublicKey:
		return policy.goodKeyECDSA(*t)
	default:
		return fmt.Errorf("Unknown key type %s", reflect.TypeOf(key))
	}
}

// GoodKeyECDSA determines if an ECDSA pubkey meets our requirements
func (policy *KeyPolicy) goodKeyECDSA(key ecdsa.PublicKey) (err error) {
	// Check the curve.
	//
	// The validity of the curve is an assumption for all following tests.
	err = policy.goodCurve(key.Curve)
	if err != nil {
		return err
	}

	// Key validation routine adapted from NIST SP800-56A § 5.6.2.3.2.
	// <http://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-56Ar2.pdf>
	//
	// Assuming a prime field since a) we are only allowing such curves and b)
	// crypto/elliptic only supports prime curves. Where this assumption
	// simplifies the code below, it is explicitly stated and explained. If ever
	// adapting this code to support non-prime curves, refer to NIST SP800-56A §
	// 5.6.2.3.2 and adapt this code appropriately.
	params := key.Params()

	// SP800-56A § 5.6.2.3.2 Step 1.
	// Partial check of the public key for an invalid range in the EC group:
	// Verify that key is not the point at infinity O.
	// This code assumes that the point at infinity is (0,0), which is the
	// case for all supported curves.
	if isPointAtInfinityNISTP(key.X, key.Y) {
		return fmt.Errorf("Key x, y must not be the point at infinity")
	}

	// SP800-56A § 5.6.2.3.2 Step 2.
	//   "Verify that x_Q and y_Q are integers in the interval [0,p-1] in the
	//    case that q is an odd prime p, or that x_Q and y_Q are bit strings
	//    of length m bits in the case that q = 2**m."
	//
	// Prove prime field: ASSUMED.
	// Prove q != 2: ASSUMED. (Curve parameter. No supported curve has q == 2.)
	// Prime field && q != 2  => q is an odd prime p
	// Therefore "verify that x, y are in [0, p-1]" satisfies step 2.
	//
	// Therefore verify that both x and y of the public key point have the unique
	// correct representation of an element in the underlying field by verifying
	// that x and y are integers in [0, p-1].
	if key.X.Sign() < 0 || key.Y.Sign() < 0 {
		return fmt.Errorf("Key x, y must not be negative")
	}

	if key.X.Cmp(params.P) >= 0 || key.Y.Cmp(params.P) >= 0 {
		return fmt.Errorf("Key x, y must not exceed P-1")
	}

	// SP800-56A § 5.6.2.3.2 Step 3.
	//   "If q is an odd prime p, verify that (y_Q)**2 === (x_Q)***3 + a*x_Q + b (mod p).
	//    If q = 2**m, verify that (y_Q)**2 + (x_Q)*(y_Q) == (x_Q)**3 + a*(x_Q)*2 + b in
	//    the finite field of size 2**m.
	//    (Ensures that the public key is on the correct elliptic curve.)"
	//
	// q is an odd prime p: proven/assumed above.
	// a = -3 for all supported curves.
	//
	// Therefore step 3 is satisfied simply by showing that
	//   y**2 === x**3 - 3*x + B (mod P).
	//
	// This proves that the public key is on the correct elliptic curve.
	// But in practice, this test is provided by crypto/elliptic, so use that.
	if !key.Curve.IsOnCurve(key.X, key.Y) {
		return fmt.Errorf("Key point is not on the curve")
	}

	// SP800-56A § 5.6.2.3.2 Step 4.
	//   "Verify that n*Q == O.
	//    (Ensures that the public key has the correct order. Along with check 1,
	//     ensures that the public key is in the correct range in the correct EC
	//     subgroup, that is, it is in the correct EC subgroup and is not the
	//     identity element.)"
	//
	// Ensure that public key has the correct order:
	// verify that n*Q = O.
	//
	// n*Q = O iff n*Q is the point at infinity (see step 1).
	ox, oy := key.Curve.ScalarMult(key.X, key.Y, params.N.Bytes())
	if !isPointAtInfinityNISTP(ox, oy) {
		return fmt.Errorf("Public key does not have correct order")
	}

	// End of SP800-56A § 5.6.2.3.2 Public Key Validation Routine.
	// Key is valid.
	return nil
}

// Returns true iff the point (x,y) on NIST P-256, NIST P-384 or NIST P-521 is
// the point at infinity. These curves all have the same point at infinity
// (0,0). This function must ONLY be used on points on curves verified to have
// (0,0) as their point at infinity.
func isPointAtInfinityNISTP(x, y *big.Int) bool {
	return x.Sign() == 0 && y.Sign() == 0
}

// GoodCurve determines if an elliptic curve meets our requirements.
func (policy *KeyPolicy) goodCurve(c elliptic.Curve) (err error) {
	// Simply use a whitelist for now.
	params := c.Params()
	switch {
	case policy.AllowECDSANISTP256 && params == elliptic.P256().Params():
		return nil
	case policy.AllowECDSANISTP384 && params == elliptic.P384().Params():
		return nil
	default:
		return fmt.Errorf(fmt.Sprintf("ECDSA curve %v not allowed", params.Name))
	}
}

// GoodKeyRSA determines if a RSA pubkey meets our requirements
func (policy *KeyPolicy) goodKeyRSA(key rsa.PublicKey) (err error) {
	if !policy.AllowRSA {
		return fmt.Errorf("RSA keys are not allowed")
	}

	// Baseline Requirements Appendix A
	// Modulus must be >= 2048 bits
	// Removed max keySize from original package version
	modulus := key.N
	modulusBitLen := modulus.BitLen()
	if modulusBitLen < 2048 {
		return fmt.Errorf(fmt.Sprintf("Key too small: %d", modulusBitLen))
	}
	// Bit lengths that are not a multiple of 8 may cause problems on some
	// client implementations.
	if modulusBitLen%8 != 0 {
		return fmt.Errorf(fmt.Sprintf("Key length wasn't a multiple of 8: %d", modulusBitLen))
	}
	// The CA SHALL confirm that the value of the public exponent is an
	// odd number equal to 3 or more. Additionally, the public exponent
	// SHOULD be in the range between 2^16 + 1 and 2^256-1.
	// NOTE: rsa.PublicKey cannot represent an exponent part greater than
	// 2^32 - 1 or 2^64 - 1, because it stores E as an integer. So we
	// don't need to check the upper bound.
	if (key.E%2) == 0 || key.E < ((1<<16)+1) {
		return fmt.Errorf(fmt.Sprintf("Key exponent should be odd and >2^16: %d", key.E))
	}
	// The modulus SHOULD also have the following characteristics: an odd
	// number, not the power of a prime, and have no factors smaller than 752.
	// TODO: We don't yet check for "power of a prime."
	if checkSmallPrimes(modulus) {
		return fmt.Errorf("Key divisible by small prime")
	}

	return nil
}

// Returns true iff integer i is divisible by any of the primes in smallPrimes.
//
// Short circuits; execution time is dependent on i. Do not use this on secret
// values.
func checkSmallPrimes(i *big.Int) bool {
	smallPrimesSingleton.Do(func() {
		for _, prime := range smallPrimeInts {
			smallPrimes = append(smallPrimes, big.NewInt(prime))
		}
	})

	for _, prime := range smallPrimes {
		var result big.Int
		result.Mod(i, prime)
		if result.Sign() == 0 {
			return true
		}
	}

	return false
}
