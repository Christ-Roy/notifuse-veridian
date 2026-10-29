// Package policy est le module Veridian « politique d'envoi et stock » (lot L3, 29/10/2026).
//
// Il porte quatre capacités, toutes pilotables par API et CLI :
//
//  1. les règles d'envoi par classe de fournisseur destinataire, en données (catalogue de
//     presets embarqué dans internal/domain, surcharges par workspace en base), avec une
//     explication lisible de chaque règle (policy.get, policy.set, policy.reset) ;
//  2. la priorité d'envoi d'une campagne ou d'une automation (policy.priority.*) ;
//  3. la vue de stock par classe, jours de couverture et besoin de réapprovisionnement
//     (stock.overview) ;
//  4. la suggestion d'inscription par classe pour tenir N jours sans dépasser les plafonds
//     (policy.suggest-enrollment). Rien n'est jamais inscrit automatiquement.
//
// Le code de la file n'est pas modifié : le worker lit la politique par l'interface
// domain.VeridianPolicyProvider et la priorité par domain.VeridianSendPriorityProvider, que ce
// module implémente (Resolver) et que le câblage branche.
package policy
