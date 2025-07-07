# Database Tools for Cosmos SDK

## Outils disponibles

### 1. Validation (`validate_cmd.go`)
Valide la cohérence entre les trois bases de données LevelDB.

```bash
go run validate_cmd.go [height] --home ~/.heliades
```

**Exemple :**
```bash
go run validate_cmd.go 31 --home ~/.heliades
```

### 2. Export d'état (`export_cmd.go`)
Exporte l'état complet de la blockchain à une hauteur donnée.

```bash
go run export_cmd.go [height] --home ~/.heliades
```

**Exemple :**
```bash
go run export_cmd.go 31 --home ~/.heliades
```

## Fichiers générés

- `complete_state_[height].json` : État complet exporté
- Contient : Block, ConsensusState, AppState, ValidatorSet, ConsensusParams

## Structure des données exportées

```json
{
  "height": 31,
  "block": { ... },
  "consensus_state": { ... },
  "app_state": { ... },
  "validator_set": { ... },
  "consensus_params": { ... }
}
``` 