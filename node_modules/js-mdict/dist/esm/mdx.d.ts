import { Mdict } from './mdict.js';
import { KeyWordItem } from '../src/mdict-base.js';
export interface FuzzyWord extends KeyWordItem {
    recordStartOffset: number;
    recordEndOffset: number;
    keyText: string;
    keyBlockIdx: number;
    ed: number;
}
export declare class MDX extends Mdict {
    /**
     * lookup the word
     * @tests ok
     * @param word search word
     * @returns word definition
     */
    lookup(word: string): {
        keyText: string;
        definition: string | null;
    };
    fetch(keywordItem: KeyWordItem): {
        keyText: string;
        definition: string | null;
    };
    /**
     * search the prefix like the phrase in the dictionary
     * @tests ok
     * @param prefix prefix search phrase
     * @returns the prefix related list
     */
    prefix(prefix: string): KeyWordItem[];
    /**
     * search matched list of associate words
     * @tests ok
     * @param phrase associate search likely workds
     * @returns matched list
     */
    associate(phrase: string): KeyWordItem[];
    /**
     * suggest the phrase with the edit distance
     * @tests ok
     * @param phrase search phrase
     * @param distance edit distance
     * @returns the suggest list
     */
    suggest(phrase: string, distance: number): KeyWordItem[];
    fetch_definition(keywordItem: KeyWordItem): {
        keyText: string;
        definition: string | null;
    };
    /**
     * fuzzy search words list
     * @tests ok
     * @param word search word
     * @param fuzzy_size the fuzzy workd size
     * @param ed_gap edit distance
     * @returns fuzzy word list
     */
    fuzzy_search(word: string, fuzzy_size: number, ed_gap: number): FuzzyWord[];
}
