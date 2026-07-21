package errors_test

import (
	stderrors "errors"
	"testing"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
)

func TestErrorMessageExcludesCause(t *testing.T) {
	cause := stderrors.New("ssh: handshake failed: connection reset by peer at 10.0.0.5:22")
	err := errs.Wrap(errs.KindSSHConnection, "sshclient.Dial", "no se pudo conectar al servidor remoto", cause)

	if got := err.Error(); got != "no se pudo conectar al servidor remoto" {
		t.Fatalf("Error() leaked internal detail or changed: %q", got)
	}

	if stderrors.Unwrap(err) == nil {
		t.Fatalf("expected Unwrap to expose the cause for logging")
	}
}

func TestKindOfAndIs(t *testing.T) {
	err := errs.New(errs.KindConflict, "transfer.commit", "el archivo destino ya existe")

	if errs.KindOf(err) != errs.KindConflict {
		t.Fatalf("KindOf() = %v, want %v", errs.KindOf(err), errs.KindConflict)
	}

	if !errs.Is(err, errs.KindConflict) {
		t.Fatalf("Is(err, KindConflict) = false, want true")
	}

	if errs.Is(err, errs.KindTimeout) {
		t.Fatalf("Is(err, KindTimeout) = true, want false")
	}
}

func TestKindOfNonDomainError(t *testing.T) {
	if got := errs.KindOf(stderrors.New("boom")); got != errs.KindInternal {
		t.Fatalf("KindOf(plain error) = %v, want %v", got, errs.KindInternal)
	}
}

func TestIsMatchesWrappedChain(t *testing.T) {
	inner := errs.New(errs.KindTimeout, "sftpclient.Open", "tiempo de espera agotado")
	outer := stderrors.Join(stderrors.New("context"), inner)

	if !errs.Is(outer, errs.KindTimeout) {
		t.Fatalf("Is() should find the wrapped domain error through errors.As chains")
	}
}
