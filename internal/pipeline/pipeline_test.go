package pipeline

import "testing"

func TestCountActions(t *testing.T) {
	notes := `**Résumé** : point budget.

**Décisions** :
- Licences sur un an.

**Actions** :
- [ ] Préparer la demande de transfert (interlocuteur)
  - [ ] Sous-tâche indentée
- [x] Déjà fait, ne compte pas
- [ ] Prévenir le fournisseur (Clément)
`
	if got, want := CountActions(notes), 3; got != want {
		t.Errorf("CountActions = %d, want %d (checked boxes and prose must not count)", got, want)
	}
	if got := CountActions(""); got != 0 {
		t.Errorf("CountActions(\"\") = %d, want 0", got)
	}
	if got := CountActions("- [x] tout est fait"); got != 0 {
		t.Errorf("CountActions with only checked boxes = %d, want 0", got)
	}
}
