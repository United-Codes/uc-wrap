create or replace package body ucw_strings as

  -- Reverses a string character by character.
  function reverse_text (p_text in varchar2) return varchar2
  is
    l_result varchar2(32767);
    l_len    pls_integer := length(p_text);
  begin
    if p_text is null then
      raise e_empty_input;
    end if;
    for i in reverse 1 .. l_len loop
      l_result := l_result || substr(p_text, i, 1);
    end loop;
    return l_result;
  end reverse_text;

  function count_words (p_text in varchar2) return pls_integer
  is
    l_count   pls_integer := 0;
    l_in_word boolean := false;
    l_char    varchar2(1);
  begin
    for i in 1 .. nvl(length(p_text), 0) loop
      l_char := substr(p_text, i, 1);
      if l_char in (' ', chr(9), chr(10), chr(13)) then
        l_in_word := false;
      elsif not l_in_word then
        l_in_word := true;
        l_count   := l_count + 1;
      end if;
    end loop;
    return l_count;
  end count_words;

  function join_parts (p_a in varchar2, p_b in varchar2, p_sep in varchar2 default gc_default_sep) return varchar2
  is
    l_text varchar2(100) := 'a -- not a comment /* nor this */ and it''s quoted';
  begin
    return case
             when p_a is null then p_b
             when p_b is null then p_a
             else p_a || p_sep || p_b
           end;
  end join_parts;

  function quote_literal (p_text in varchar2) return varchar2
  is
  begin
    return '''' || replace(p_text, '''', '''''') || '''';
  end quote_literal;

end ucw_strings;
/
