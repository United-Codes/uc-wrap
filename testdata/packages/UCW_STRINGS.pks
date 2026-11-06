create or replace package ucw_strings as
  gc_default_sep constant varchar2(1) := ',';

  e_empty_input exception;

  function reverse_text (p_text in varchar2) return varchar2;
  function count_words (p_text in varchar2) return pls_integer;
  function join_parts (p_a in varchar2, p_b in varchar2, p_sep in varchar2 default gc_default_sep) return varchar2;
  function quote_literal (p_text in varchar2) return varchar2;
end ucw_strings;
/
